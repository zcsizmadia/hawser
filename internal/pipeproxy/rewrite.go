package pipeproxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/zcsizmadia/hawser/internal/winpath"
)

// RewriteBinds is a Server.Handler that translates Windows bind paths on their
// way to the engine, then gets out of the way.
//
// Spike A (#2) established why this is necessary and where it must happen: the
// daemon rejects "C:\src:/app" itself, and the CLI forwards the spec untouched,
// so the proxy is the only place left. It must also be surgical — every other
// byte has to pass through unaltered, because Docker hijacks connections for
// exec, attach, and logs, and a hijacked stream is not HTTP at all.
//
// The handler therefore proxies HTTP only until the engine signals a hijack,
// then reverts to a raw byte relay for the life of the connection.
func RewriteBinds(client net.Conn, engine io.ReadWriteCloser) error {
	clientR := bufio.NewReader(client)
	engineR := bufio.NewReader(engine)

	for {
		req, err := http.ReadRequest(clientR)
		if err != nil {
			// EOF is the ordinary end of a keep-alive connection.
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("read request: %w", err)
		}

		if isContainerCreate(req) {
			if err := rewriteCreateBody(req); err != nil {
				// Refusing is better than forwarding a mount the user did not
				// ask for; report it as the API would.
				return writeError(client, http.StatusBadRequest, err)
			}
		}

		if err := req.Write(engine); err != nil {
			return fmt.Errorf("forward request: %w", err)
		}

		resp, err := http.ReadResponse(engineR, req)
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}

		if isHijack(resp) {
			// From here the connection carries a raw multiplexed stream, so
			// hand back the headers verbatim and stop parsing entirely.
			if err := writeResponseHead(client, resp); err != nil {
				resp.Body.Close()
				return err
			}
			return relayBuffered(client, engine, clientR, engineR)
		}

		// resp.Write streams the body as it arrives, which is what keeps
		// `docker logs -f` and pull progress incremental.
		if err := resp.Write(client); err != nil {
			resp.Body.Close()
			return fmt.Errorf("forward response: %w", err)
		}
		resp.Body.Close()

		if resp.Close || req.Close {
			return nil
		}
	}
}

// containerCreatePath matches /containers/create with or without the version
// prefix the CLI normally sends (/v1.55/containers/create).
var containerCreatePath = regexp.MustCompile(`^(/v[0-9.]+)?/containers/create$`)

func isContainerCreate(req *http.Request) bool {
	return req.Method == http.MethodPost && containerCreatePath.MatchString(req.URL.Path)
}

// hijackContentTypes are the media types dockerd uses for raw and multiplexed
// container streams. Docker does not always answer an upgrade with 101: when
// the client does not request an upgrade, exec and attach return 200 and then
// stream, so status code alone is not enough to detect a hijack.
var hijackContentTypes = []string{
	"application/vnd.docker.raw-stream",
	"application/vnd.docker.multiplexed-stream",
}

func isHijack(resp *http.Response) bool {
	if resp.StatusCode == http.StatusSwitchingProtocols {
		return true
	}
	ct := resp.Header.Get("Content-Type")
	for _, h := range hijackContentTypes {
		if strings.HasPrefix(ct, h) {
			return true
		}
	}
	return false
}

// rewriteCreateBody translates bind paths in a container-create request.
//
// The body is decoded into a generic map rather than a typed struct so that
// fields this code does not know about survive untouched — the engine API grows
// every release, and silently dropping a caller's option would be far worse
// than not translating a path. json.Number likewise preserves numeric literals
// exactly instead of round-tripping them through float64.
func rewriteCreateBody(req *http.Request) error {
	if req.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return fmt.Errorf("read create body: %w", err)
	}
	if len(raw) == 0 {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		// Not JSON we understand: pass it through and let the engine judge it.
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return nil
	}

	changed, err := translateHostConfig(body)
	if err != nil {
		return err
	}
	if !changed {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return nil
	}

	out, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("re-encode create body: %w", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(out))
	req.ContentLength = int64(len(out))
	// The rewrite changes the length, so a stale Content-Length header would
	// desynchronize the stream. Drop any chunked framing for the same reason.
	req.Header.Del("Content-Length")
	req.TransferEncoding = nil
	return nil
}

// translateHostConfig rewrites HostConfig.Binds and the source of any bind-type
// entry in HostConfig.Mounts, reporting whether anything changed.
func translateHostConfig(body map[string]any) (bool, error) {
	hc, ok := body["HostConfig"].(map[string]any)
	if !ok {
		return false, nil
	}

	var changed bool

	if rawBinds, ok := hc["Binds"].([]any); ok {
		binds := make([]string, 0, len(rawBinds))
		for _, b := range rawBinds {
			s, ok := b.(string)
			if !ok {
				return false, fmt.Errorf("HostConfig.Binds contains a non-string entry")
			}
			binds = append(binds, s)
		}
		translated, err := winpath.TranslateBinds(binds)
		if err != nil {
			return false, err
		}
		for i := range translated {
			if translated[i] != binds[i] {
				changed = true
			}
		}
		if changed {
			next := make([]any, len(translated))
			for i, s := range translated {
				next[i] = s
			}
			hc["Binds"] = next
		}
	}

	// --mount syntax, and what compose emits for long-form volumes.
	if mounts, ok := hc["Mounts"].([]any); ok {
		for _, m := range mounts {
			mount, ok := m.(map[string]any)
			if !ok {
				continue
			}
			// Only bind mounts name a host path; a volume's Source is a name.
			if t, _ := mount["Type"].(string); t != "bind" {
				continue
			}
			src, ok := mount["Source"].(string)
			if !ok || src == "" {
				continue
			}
			translated, err := winpath.ToWSL(src)
			if err != nil {
				return false, err
			}
			if translated != src {
				mount["Source"] = translated
				changed = true
			}
		}
	}

	return changed, nil
}

// writeResponseHead emits a status line and headers without touching the body,
// used when the connection is about to become a raw stream.
func writeResponseHead(w io.Writer, resp *http.Response) error {
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode))
	if err := resp.Header.Write(&b); err != nil {
		return err
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write hijack response head: %w", err)
	}
	return nil
}

// writeError reports a proxy-side refusal in the shape the Docker API uses, so
// the CLI prints a real message instead of "error during connect".
func writeError(w io.Writer, status int, cause error) error {
	payload, err := json.Marshal(map[string]string{"message": cause.Error()})
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	b.WriteString("Content-Type: application/json\r\n")
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(payload))
	b.WriteString("Connection: close\r\n\r\n")
	b.Write(payload)
	if _, werr := io.WriteString(w, b.String()); werr != nil {
		return werr
	}
	return nil
}

// relayBuffered switches to a raw byte relay, first flushing whatever the HTTP
// readers already pulled off the wire. Skipping that would silently swallow the
// first bytes of a hijacked stream — the kind of bug that looks like a hung
// `docker exec` rather than a lost buffer.
func relayBuffered(client net.Conn, engine io.ReadWriteCloser, clientR, engineR *bufio.Reader) error {
	if n := engineR.Buffered(); n > 0 {
		buf, err := engineR.Peek(n)
		if err != nil {
			return err
		}
		if _, err := client.Write(buf); err != nil {
			return err
		}
		engineR.Discard(n)
	}
	if n := clientR.Buffered(); n > 0 {
		buf, err := clientR.Peek(n)
		if err != nil {
			return err
		}
		if _, err := engine.Write(buf); err != nil {
			return err
		}
		clientR.Discard(n)
	}
	return Relay(client, engine)
}
