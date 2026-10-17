package httpx

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestListRotateEveryRequest(t *testing.T) {
	g := mustGate(t, ProxyConfig{
		List:  []string{"http://1.1.1.1:8080", "http://2.2.2.2:8080"},
		Every: 1,
	})
	a := mustCurrent(t, g)
	b := mustCurrent(t, g)
	c := mustCurrent(t, g)
	if a.Host != "1.1.1.1:8080" || b.Host != "2.2.2.2:8080" || c.Host != "1.1.1.1:8080" {
		t.Fatalf("%s %s %s", a.Host, b.Host, c.Host)
	}
	if g.label() != "http://1.1.1.1:8080" {
		t.Fatal(g.label())
	}
}

func TestEveryKeepsThenSwitches(t *testing.T) {
	g := mustGate(t, ProxyConfig{
		List:  []string{"http://1.1.1.1:1", "http://2.2.2.2:2"},
		Every: 2,
	})
	a := mustCurrent(t, g)
	b := mustCurrent(t, g)
	c := mustCurrent(t, g)
	if a.Host != b.Host || a.Host == c.Host {
		t.Fatalf("%s %s %s", a.Host, b.Host, c.Host)
	}
}

func TestInvalidateDropsIP(t *testing.T) {
	g := mustGate(t, ProxyConfig{
		List:  []string{"http://1.1.1.1:1", "http://2.2.2.2:2"},
		Every: 20,
	})
	a := mustCurrent(t, g)
	g.invalidate()
	b := mustCurrent(t, g)
	if a.Host == b.Host {
		t.Fatalf("still %s", a.Host)
	}
}

func TestFixedProxyIgnoresInvalidate(t *testing.T) {
	g := mustGate(t, ProxyConfig{URL: "http://user:secret@1.1.1.1:7890"})
	a := mustCurrent(t, g)
	g.invalidate()
	b := mustCurrent(t, g)
	if a.String() != b.String() {
		t.Fatalf("%s %s", a, b)
	}
	if g.label() != "http://1.1.1.1:7890" {
		t.Fatal(g.label())
	}
	if g.rotates() {
		t.Fatal("fixed proxy should not rotate")
	}
}

func TestAPIReturnsLines(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprint(w, "10.0.0.1:8000\n10.0.0.2:8000")
	}))
	defer srv.Close()

	g := mustGate(t, ProxyConfig{API: srv.URL, Every: 1, Username: "u", Password: "p"})
	a := mustCurrent(t, g)
	b := mustCurrent(t, g)
	if a.Host != "10.0.0.1:8000" || b.Host != "10.0.0.2:8000" {
		t.Fatalf("%s %s", a.Host, b.Host)
	}
	user := a.User.Username()
	pass, _ := a.User.Password()
	if user != "u" || pass != "p" {
		t.Fatalf("auth %s %s", user, pass)
	}
	if g.label() != "http://10.0.0.2:8000" {
		t.Fatal(g.label())
	}
	if hits != 1 {
		t.Fatalf("fetches=%d", hits)
	}
}

func TestParseProxyJSON(t *testing.T) {
	body := []byte(`{"data":[{"ip":"1.2.3.4","port":8080},{"host":"5.6.7.8","port":"9090"}]}`)
	urls, err := parseProxyBody(body, "socks5", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 || urls[0].String() != "socks5://1.2.3.4:8080" || urls[1].Host != "5.6.7.8:9090" {
		t.Fatalf("%v", urls)
	}
	urls, err = parseProxyBody([]byte(`{"data":"9.9.9.9:1, 8.8.8.8:2"}`), "http", "", "")
	if err != nil || len(urls) != 2 {
		t.Fatalf("%v %v", urls, err)
	}
	if _, err := parseProxyBody([]byte(`{"code":1}`), "http", "", ""); err == nil {
		t.Fatal("expected empty payload error")
	}
	if _, err := parseProxyBody([]byte("   "), "http", "", ""); err == nil {
		t.Fatal("expected empty body error")
	}
}

func TestMixedProxySchemes(t *testing.T) {
	if _, err := newGate(ProxyConfig{List: []string{"http://1.1.1.1:1", "socks5://2.2.2.2:2"}}); err == nil {
		t.Fatal("expected mixed scheme error")
	}
}

func TestOnSwitch(t *testing.T) {
	g := mustGate(t, ProxyConfig{
		List:  []string{"http://1.1.1.1:1", "http://2.2.2.2:2"},
		Every: 1,
	})
	var notes []string
	g.notify = func(s string) { notes = append(notes, s) }
	mustCurrent(t, g)
	mustCurrent(t, g)
	if len(notes) != 2 || notes[0] != "http://1.1.1.1:1" || notes[1] != "http://2.2.2.2:2" {
		t.Fatalf("%v", notes)
	}
}

func mustGate(t *testing.T, px ProxyConfig) *gate {
	t.Helper()
	g, err := newGate(px)
	if err != nil {
		t.Fatal(err)
	}
	if g == nil {
		t.Fatal("nil gate")
	}
	return g
}

func mustCurrent(t *testing.T, g *gate) *url.URL {
	t.Helper()
	got, err := g.current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}
