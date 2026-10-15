// Package wsd implementa WS-Discovery (Devices Profile for Web Services) para que el
// NAS aparezca en «Red» del Explorador de Windows (protocol/README.md §14). Responde
// Probe/Resolve por UDP 3702 multicast, anuncia Hello/Bye y sirve los metadatos
// (WS-Transfer Get) por HTTP. Go nativo, sin wsdd (ADR 0009).
package wsd

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Constantes del protocolo.
const (
	MulticastAddr = "239.255.255.250"
	Port          = 3702
	HTTPPort      = 5357

	nsSoap = "http://www.w3.org/2003/05/soap-envelope"
	nsWSA  = "http://schemas.xmlsoap.org/ws/2004/08/addressing"
	nsWSD  = "http://schemas.xmlsoap.org/ws/2005/04/discovery"
	nsWSX  = "http://schemas.xmlsoap.org/ws/2004/09/mex"
	nsWSDP = "http://schemas.xmlsoap.org/ws/2006/02/devprof"
	nsPNPX = "http://schemas.microsoft.com/windows/pnpx/2005/10"
	nsPub  = "http://schemas.microsoft.com/windows/pub/2005/07"

	toDiscovery = "urn:schemas-xmlsoap-org:ws:2005:04:discovery"
	toAnonymous = "http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous"

	ActHello        = nsWSD + "/Hello"
	ActBye          = nsWSD + "/Bye"
	ActProbe        = nsWSD + "/Probe"
	ActProbeMatches = nsWSD + "/ProbeMatches"
	ActResolve      = nsWSD + "/Resolve"
	ActResolveMatch = nsWSD + "/ResolveMatches"
	ActGet          = "http://schemas.xmlsoap.org/ws/2004/09/transfer/Get"
	ActGetResponse  = "http://schemas.xmlsoap.org/ws/2004/09/transfer/GetResponse"

	deviceTypes = "wsdp:Device pub:Computer"
)

// UUIDFromID deriva un UUID estable (formato v5) de un identificador del NAS.
func UUIDFromID(id string) string {
	h := sha1.Sum([]byte("tangoos wsd\n" + id))
	h[6] = (h[6] & 0x0f) | 0x50
	h[8] = (h[8] & 0x3f) | 0x80
	x := hex.EncodeToString(h[:16])
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

func newMessageID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	x := hex.EncodeToString(b)
	return "urn:uuid:" + x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Message es lo que interesa de un mensaje SOAP entrante.
type Message struct {
	Action    string
	MessageID string
	RelatesTo string
	Types     string   // Probe: tipos pedidos
	Addresses []string // Resolve / matches: EndpointReference/Address
	XAddrs    string
	Computer  string // GetResponse: pub:Computer
}

// Parse lee un sobre SOAP por nombres locales (ignora prefijos).
func Parse(b []byte) (Message, error) {
	var m Message
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	var path []string
	sawEnvelope := false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			path = append(path, t.Name.Local)
			if t.Name.Local == "Envelope" {
				sawEnvelope = true
			}
		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.CharData:
			if len(path) == 0 {
				continue
			}
			v := strings.TrimSpace(string(t))
			if v == "" {
				continue
			}
			switch path[len(path)-1] {
			case "Action":
				m.Action = v
			case "MessageID":
				m.MessageID = v
			case "RelatesTo":
				m.RelatesTo = v
			case "Types":
				m.Types = v
			case "Address":
				if len(path) >= 2 && path[len(path)-2] == "EndpointReference" {
					m.Addresses = append(m.Addresses, v)
				}
			case "XAddrs":
				m.XAddrs = v
			case "Computer":
				m.Computer = v
			}
		}
	}
	if !sawEnvelope {
		return m, fmt.Errorf("no es un sobre SOAP")
	}
	return m, nil
}

// Device describe este NAS.
type Device struct {
	UUID       string // sin "urn:uuid:"
	Hostname   string
	Workgroup  string
	InstanceID int64
}

func (d Device) urn() string { return "urn:uuid:" + d.UUID }

// XAddr es la URL de metadatos para una IP.
func (d Device) XAddr(ip string) string { return fmt.Sprintf("http://%s:%d/%s", ip, HTTPPort, d.UUID) }

func envelope(action, to, msgID, relatesTo, extraHeader, body string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	fmt.Fprintf(&b, `<soap:Envelope xmlns:soap="%s" xmlns:wsa="%s" xmlns:wsd="%s" xmlns:wsx="%s" xmlns:wsdp="%s" xmlns:pnpx="%s" xmlns:pub="%s">`,
		nsSoap, nsWSA, nsWSD, nsWSX, nsWSDP, nsPNPX, nsPub)
	fmt.Fprintf(&b, `<soap:Header><wsa:To>%s</wsa:To><wsa:Action>%s</wsa:Action><wsa:MessageID>%s</wsa:MessageID>`, to, action, msgID)
	if relatesTo != "" {
		fmt.Fprintf(&b, `<wsa:RelatesTo>%s</wsa:RelatesTo>`, esc(relatesTo))
	}
	b.WriteString(extraHeader)
	b.WriteString(`</soap:Header><soap:Body>`)
	b.WriteString(body)
	b.WriteString(`</soap:Body></soap:Envelope>`)
	return []byte(b.String())
}

func (d Device) appSeq(n int64) string {
	return fmt.Sprintf(`<wsd:AppSequence InstanceId="%d" SequenceId="%s" MessageNumber="%d"/>`, d.InstanceID, d.urn(), n)
}

func (d Device) epr() string {
	return `<wsa:EndpointReference><wsa:Address>` + d.urn() + `</wsa:Address></wsa:EndpointReference>`
}

// Hello anuncia el NAS (multicast).
func (d Device) Hello(ip string, n int64) []byte {
	body := `<wsd:Hello>` + d.epr() + `<wsd:Types>` + deviceTypes + `</wsd:Types><wsd:XAddrs>` + d.XAddr(ip) +
		`</wsd:XAddrs><wsd:MetadataVersion>1</wsd:MetadataVersion></wsd:Hello>`
	return envelope(ActHello, toDiscovery, newMessageID(), "", d.appSeq(n), body)
}

// Bye avisa que el NAS se va (multicast).
func (d Device) Bye(n int64) []byte {
	body := `<wsd:Bye>` + d.epr() + `</wsd:Bye>`
	return envelope(ActBye, toDiscovery, newMessageID(), "", d.appSeq(n), body)
}

// wantsUs dice si un Probe pide tipos que el NAS cumple (sin tipos = todos).
func wantsUs(types string) bool {
	if strings.TrimSpace(types) == "" {
		return true
	}
	for _, t := range strings.Fields(types) {
		local := t
		if i := strings.LastIndex(t, ":"); i >= 0 {
			local = t[i+1:]
		}
		if local == "Device" || local == "Computer" {
			return true
		}
	}
	return false
}

// Reply arma la respuesta a un mensaje recibido por UDP (nil si no corresponde).
func (d Device) Reply(m Message, ip string, n int64) []byte {
	switch m.Action {
	case ActProbe:
		if !wantsUs(m.Types) {
			return nil
		}
		body := `<wsd:ProbeMatches><wsd:ProbeMatch>` + d.epr() + `<wsd:Types>` + deviceTypes + `</wsd:Types><wsd:XAddrs>` +
			d.XAddr(ip) + `</wsd:XAddrs><wsd:MetadataVersion>1</wsd:MetadataVersion></wsd:ProbeMatch></wsd:ProbeMatches>`
		return envelope(ActProbeMatches, toAnonymous, newMessageID(), m.MessageID, d.appSeq(n), body)
	case ActResolve:
		ok := false
		for _, a := range m.Addresses {
			if strings.EqualFold(a, d.urn()) {
				ok = true
			}
		}
		if !ok {
			return nil
		}
		body := `<wsd:ResolveMatches><wsd:ResolveMatch>` + d.epr() + `<wsd:Types>` + deviceTypes + `</wsd:Types><wsd:XAddrs>` +
			d.XAddr(ip) + `</wsd:XAddrs><wsd:MetadataVersion>1</wsd:MetadataVersion></wsd:ResolveMatch></wsd:ResolveMatches>`
		return envelope(ActResolveMatch, toAnonymous, newMessageID(), m.MessageID, d.appSeq(n), body)
	}
	return nil
}

// GetResponse son los metadatos del dispositivo (WS-Transfer Get por HTTP).
func (d Device) GetResponse(relatesTo string) []byte {
	host := esc(d.Hostname)
	wg := esc(strings.ToUpper(d.Workgroup))
	body := `<wsx:Metadata>` +
		`<wsx:MetadataSection Dialect="` + nsWSDP + `/ThisDevice"><wsdp:ThisDevice><wsdp:FriendlyName>TangoOS ` + host +
		`</wsdp:FriendlyName><wsdp:FirmwareVersion>1.0</wsdp:FirmwareVersion><wsdp:SerialNumber>1</wsdp:SerialNumber></wsdp:ThisDevice></wsx:MetadataSection>` +
		`<wsx:MetadataSection Dialect="` + nsWSDP + `/ThisModel"><wsdp:ThisModel><wsdp:Manufacturer>TangoOS</wsdp:Manufacturer>` +
		`<wsdp:ModelName>TangoOS NAS</wsdp:ModelName><pnpx:DeviceCategory>Computers</pnpx:DeviceCategory></wsdp:ThisModel></wsx:MetadataSection>` +
		`<wsx:MetadataSection Dialect="` + nsWSDP + `/Relationship"><wsdp:Relationship Type="` + nsWSDP + `/host"><wsdp:Host>` + d.epr() +
		`<wsdp:Types>pub:Computer</wsdp:Types><wsdp:ServiceId>` + d.urn() + `</wsdp:ServiceId><pub:Computer>` + host + `/Workgroup:` + wg +
		`</pub:Computer></wsdp:Host></wsdp:Relationship></wsx:MetadataSection></wsx:Metadata>`
	return envelope(ActGetResponse, toAnonymous, newMessageID(), relatesTo, "", body)
}

// ProbeMessage es un Probe de cliente (para tangoctl y los tests).
func ProbeMessage() ([]byte, string) {
	id := newMessageID()
	body := `<wsd:Probe><wsd:Types>wsdp:Device</wsd:Types></wsd:Probe>`
	return envelope(ActProbe, toDiscovery, id, "", "", body), id
}

// GetMessage es un WS-Transfer Get de cliente.
func GetMessage(uuid string) ([]byte, string) {
	id := newMessageID()
	return envelope(ActGet, "urn:uuid:"+uuid, id, "", `<wsa:ReplyTo><wsa:Address>`+toAnonymous+`</wsa:Address></wsa:ReplyTo>`, ""), id
}
