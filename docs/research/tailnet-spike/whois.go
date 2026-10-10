//go:build ignore

// whois is the throwaway program of the tailnet spike (docs/research/tailnet-spike.md).
// It uses only the standard library: it asks the Tailscale already running on
// this machine (through its LocalAPI socket) for the machine's tailnet address,
// listens on that address only, and answers every request with the login name
// and device of the caller, as Tailscale reports them.
//
//	go run docs/research/tailnet-spike/whois.go [-socket PATH] [-port 7843]
//
// Then, from another device of the same tailnet:  curl http://<this-machine>:7843/
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"
)

type localAPI struct{ hc *http.Client }

func newLocalAPI(socket string) *localAPI {
	return &localAPI{hc: &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		}},
	}}
}

func (l *localAPI) get(ctx context.Context, path string, out any) (int, error) {
	// The host name is fixed by the protocol: Tailscale refuses anything else.
	req, err := http.NewRequestWithContext(ctx, "GET", "http://local-tailscaled.sock"+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := l.hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return resp.StatusCode, fmt.Errorf("LocalAPI %s: %s", path, resp.Status)
	}
	return 200, json.NewDecoder(resp.Body).Decode(out)
}

func main() {
	socket := flag.String("socket", "/var/run/tailscale/tailscaled.sock", "Tailscale LocalAPI socket")
	port := flag.String("port", "7843", "port to listen on, on the tailnet address only")
	flag.Parse()
	ctx := context.Background()
	lc := newLocalAPI(*socket)

	var st struct {
		BackendState string
		Self         struct {
			DNSName      string
			TailscaleIPs []string
		}
	}
	if _, err := lc.get(ctx, "/localapi/v0/status?peers=false", &st); err != nil {
		log.Fatalf("cannot ask Tailscale for its status (is it installed and running?): %v", err)
	}
	if st.BackendState != "Running" || len(st.Self.TailscaleIPs) == 0 {
		log.Fatalf("Tailscale is %q, not Running (run: tailscale up)", st.BackendState)
	}
	addr := net.JoinHostPort(st.Self.TailscaleIPs[0], *port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on %s (%s)", addr, st.Self.DNSName)

	http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var who struct {
			Node struct {
				ComputedName, Name string
				Tags               []string
			}
			UserProfile struct{ LoginName, DisplayName string }
		}
		code, err := lc.get(r.Context(), "/localapi/v0/whois?addr="+url.QueryEscape(r.RemoteAddr), &who)
		if err != nil {
			log.Printf("refused %s: %v (HTTP %d)", r.RemoteAddr, err, code)
			http.Error(w, "unknown caller", http.StatusForbidden)
			return
		}
		fmt.Fprintf(w, "hello %s from %s (tags %v)\n", who.UserProfile.LoginName, who.Node.ComputedName, who.Node.Tags)
	}))
}
