package template

import "winterflow/internal/domain/compose/xwinterflow"

// Detected is what a body's compose files declare under x-winterflow, as
// the editor's Detected panel and the install form show it.
type Detected struct {
	// Variables are what an instance fills in (required ones need a value;
	// encrypted ones are secrets, sent encrypted for the server).
	Variables []xwinterflow.DeclaredVariable `json:"variables"`
	// Expose are the container ports published on the host; an http one
	// can take hostnames.
	Expose []DetectedExpose `json:"expose"`
	// Backups are the backups an instance can turn on.
	Backups []DetectedBackup `json:"backups"`
	// Warnings are what the parser and the save point out.
	Warnings []string `json:"warnings"`
}

// DetectedExpose is one x-winterflow.expose entry.
type DetectedExpose struct {
	ID        string `json:"id" description:"<container>:<port>/<protocol>"`
	Target    string `json:"target" description:"<container>:<port>: what a hostname's expose names (http entries only)."`
	Container string `json:"container"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol" enum:"http,tcp,udp"`
}

// DetectedBackup is one x-winterflow.backups entry.
type DetectedBackup struct {
	Name string `json:"name"`
	Type string `json:"type" enum:"sqlite,files,custom"`
}

// Detect lists what spec declares, with warnings.
func Detect(spec xwinterflow.Spec, warnings []string) Detected {
	out := Detected{
		Variables: append([]xwinterflow.DeclaredVariable{}, spec.Variables...),
		Expose:    []DetectedExpose{},
		Backups:   []DetectedBackup{},
		Warnings:  append([]string{}, warnings...),
	}
	for _, e := range spec.Expose {
		out.Expose = append(out.Expose, DetectedExpose{ID: e.ID(), Target: e.Target(), Container: e.Container, Port: e.Port, Protocol: e.Protocol})
	}
	for _, r := range spec.Compose.Recipe {
		out.Backups = append(out.Backups, DetectedBackup{Name: r.Name, Type: string(r.Type)})
	}
	return out
}
