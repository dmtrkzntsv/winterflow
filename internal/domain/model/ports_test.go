package model

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParsePorts(t *testing.T) {
	ports, present, err := ParsePorts([]byte(`{"ports":{"app:8080/http":{"host":20013,"pinned":false},"db:5432/tcp":{"host":5432,"pinned":true}}}`))
	if err != nil || !present {
		t.Fatalf("present=%v err=%v", present, err)
	}
	want := map[string]PortAllocation{"app:8080/http": {Host: 20013}, "db:5432/tcp": {Host: 5432, Pinned: true}}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %+v", ports)
	}
	if got := HTTPPorts(ports); !reflect.DeepEqual(got, map[string]int{"app:8080": 20013}) {
		t.Fatalf("http = %v", got)
	}
	for _, blob := range []string{`{}`, `{"ports":null}`} {
		if ports, present, err := ParsePorts([]byte(blob)); err != nil || present || len(ports) != 0 {
			t.Fatalf("%s: %v %v %v", blob, ports, present, err)
		}
	}
	for blob, want := range map[string]string{
		`{"ports":{"app:80/http":{"host":0}}}`:     "app:80/http: 0 is not a port",
		`{"ports":{"app:80/http":{"host":70000}}}`: "70000 is not a port",
		`{"ports":[1]}`: "cannot unmarshal",
		`not json`:      "invalid character",
	} {
		if _, _, err := ParsePorts([]byte(blob)); !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v", blob, err)
		}
	}
}
