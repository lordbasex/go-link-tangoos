package wsd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func testDevice() Device {
	return Device{UUID: UUIDFromID("tg_02d106f27444"), Hostname: "tangoos-dev", Workgroup: "workgroup", InstanceID: 1760500000}
}

func TestUUIDStable(t *testing.T) {
	a, b := UUIDFromID("tg_x"), UUIDFromID("tg_x")
	if a != b || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(a) {
		t.Fatalf("uuid %q %q", a, b)
	}
	if UUIDFromID("tg_y") == a {
		t.Fatal("different ids must give different uuids")
	}
}

func TestProbeAndResolve(t *testing.T) {
	d := testDevice()
	probe, id := ProbeMessage()
	m, err := Parse(probe)
	if err != nil || m.Action != ActProbe || m.MessageID != id || m.Types != "wsdp:Device" {
		t.Fatalf("parse probe: %+v %v", m, err)
	}
	reply := d.Reply(m, "172.17.0.44", 3)
	r, err := Parse(reply)
	if err != nil || r.Action != ActProbeMatches || r.RelatesTo != id || len(r.Addresses) != 1 || r.Addresses[0] != "urn:uuid:"+d.UUID {
		t.Fatalf("probe match: %+v %v\n%s", r, err, reply)
	}
	if r.XAddrs != "http://172.17.0.44:5357/"+d.UUID {
		t.Fatalf("xaddrs: %q", r.XAddrs)
	}
	// Tipos que no somos: sin respuesta. Sin tipos: respuesta.
	if d.Reply(Message{Action: ActProbe, Types: "wprt:PrintDeviceType"}, "1.2.3.4", 1) != nil {
		t.Fatal("printer probe must be ignored")
	}
	if d.Reply(Message{Action: ActProbe}, "1.2.3.4", 1) == nil {
		t.Fatal("untyped probe must match")
	}
	// Resolve: solo nuestro EPR.
	if d.Reply(Message{Action: ActResolve, Addresses: []string{"urn:uuid:otro"}}, "1.2.3.4", 1) != nil {
		t.Fatal("resolve of another device must be ignored")
	}
	rr, _ := Parse(d.Reply(Message{Action: ActResolve, MessageID: "urn:uuid:x", Addresses: []string{"urn:uuid:" + d.UUID}}, "1.2.3.4", 1))
	if rr.Action != ActResolveMatch || rr.RelatesTo != "urn:uuid:x" {
		t.Fatalf("resolve match: %+v", rr)
	}
	// Hello/Bye bien formados.
	for _, b := range [][]byte{d.Hello("1.2.3.4", 1), d.Bye(2)} {
		if _, err := Parse(b); err != nil {
			t.Fatalf("hello/bye: %v", err)
		}
	}
	if _, err := Parse([]byte("hola")); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestMetadataHTTP(t *testing.T) {
	d := testDevice()
	d.Hostname = "nas<&>"
	s := &Server{Device: func() Device { return d }}
	ts := httptest.NewServer(s)
	defer ts.Close()
	get, id := GetMessage(d.UUID)
	resp, err := http.Post(ts.URL+"/"+d.UUID, "application/soap+xml", bytes.NewReader(get))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m, err := Parse(body)
	if err != nil || resp.StatusCode != 200 || m.Action != ActGetResponse || m.RelatesTo != id {
		t.Fatalf("get: %d %+v %v\n%s", resp.StatusCode, m, err, body)
	}
	if m.Computer != "nas<&>/Workgroup:WORKGROUP" || !strings.Contains(string(body), "nas&lt;&amp;&gt;") {
		t.Fatalf("computer must be escaped: %q", m.Computer)
	}
	if r, _ := http.Post(ts.URL+"/otro", "application/soap+xml", bytes.NewReader(get)); r.StatusCode != 404 {
		t.Fatalf("wrong path: %d", r.StatusCode)
	}
	if r, _ := http.Get(ts.URL + "/" + d.UUID); r.StatusCode != 404 {
		t.Fatalf("GET: %d", r.StatusCode)
	}
}
