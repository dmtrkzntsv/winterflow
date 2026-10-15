// Package xwinterflow parses and validates the x-winterflow block of an
// app's compose files: exposed ports, declared variables, overrides, extra
// files and the backup recipe (internal/domain/backup/recipe, read from the
// same decoded files, so a compose file is parsed once). The API (save-app
// 400s) and the agent (save and deploy) share it; Render turns a Spec into
// the compose override file the agent passes last.
package xwinterflow

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v3"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/varref"
)

// Mode picks how the rules that differ between apps and templates apply.
type Mode int

const (
	// ModeApp warns about a volume with a literal name.
	ModeApp Mode = iota
	// ModeTemplate refuses it: two installs would share one volume.
	ModeTemplate
)

// Protocols of an expose entry.
const (
	ProtocolHTTP = "http"
	ProtocolTCP  = "tcp"
	ProtocolUDP  = "udp"
)

// Limits of the format.
const (
	MaxPattern     = 512
	MaxDescription = 1000
	maxPath        = 1024
)

// ErrInvalid marks an x-winterflow block that can't be used; the message
// names the field. It matches model.ErrRejected (a 400).
var ErrInvalid = model.Rejected("invalid x-winterflow")

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Expose is one container port published on the host.
type Expose struct {
	Container string
	Port      int
	Protocol  string
}

// Target is the entry as an ingress names it: <container>:<port>.
func (e Expose) Target() string { return e.Container + ":" + strconv.Itoa(e.Port) }

// ID identifies the entry, <container>:<port>/<protocol>: the key of its
// host port in config.json's ports.
func (e Expose) ID() string { return e.Target() + "/" + e.Protocol }

// Bind is the host address the entry is published on: loopback for http
// (only the ingress reaches it), every interface for tcp and udp.
func (e Expose) Bind() string {
	if e.Protocol == ProtocolHTTP {
		return "127.0.0.1"
	}
	return "0.0.0.0"
}

// DeclaredVariable is one x-winterflow.variables entry, with what the
// services' environments say about it.
type DeclaredVariable struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Encrypted   bool   `json:"encrypted"`
	Pattern     string `json:"pattern,omitempty"`
	Description string `json:"description,omitempty"`
	// Default is the body literal of the first service whose environment
	// has the key: nil when none has one, and always for a secret.
	Default *string `json:"default"`
	// Services are the services whose environment has the key, in file
	// order: the ones a value is applied to.
	Services []string `json:"services"`
}

// Spec is what an app's compose files declare under x-winterflow, plus the
// compose model backups need.
type Spec struct {
	// Declared is set when a file has an x-winterflow block.
	Declared  bool
	Expose    []Expose
	Variables []DeclaredVariable
	// Overrides are the services' override fragments (service name ->
	// mapping), merged in by Render.
	Overrides map[string]*yaml.Node
	Files     []string
	Compose   recipe.Compose
	// Warnings are rules an app may break (a literal volume name, a
	// variable nothing uses).
	Warnings []string
}

var (
	nameRE       = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	exposeKeys   = map[string]bool{"container": true, "port": true, "protocol": true}
	variableKeys = map[string]bool{"required": true, "encrypted": true, "pattern": true, "description": true}
)

// Parse decodes the compose files once, in the order compose loads them
// (model.DetectComposeFiles, or a git-sourced app's compose_path), and
// validates their x-winterflow blocks. A later file's variable, override
// key or backup replaces an earlier file's of the same name; expose entries
// and files are merged. A file that is not valid YAML is skipped and sets
// Compose.Unparsed, as recipe.ParseCompose does.
func Parse(mode Mode, files ...[]byte) (Spec, error) {
	docs := recipe.DecodeFiles(files...)
	comp, err := recipe.ParseComposeNodes(docs...)
	if err != nil {
		return Spec{}, err
	}
	s := Spec{Compose: comp, Overrides: map[string]*yaml.Node{}}
	var envs []envKey
	for _, doc := range docs {
		root := rootMapping(doc)
		if root == nil {
			continue
		}
		envs = append(envs, envKeys(root)...)
		if err := s.checkVolumes(root, mode); err != nil {
			return Spec{}, err
		}
		x := value(root, "x-winterflow")
		if x == nil || x.Tag == "!!null" {
			continue
		}
		if x.Kind != yaml.MappingNode {
			return Spec{}, invalid("x-winterflow", "must be a mapping")
		}
		s.Declared = true
		if err := s.read(x); err != nil {
			return Spec{}, err
		}
	}
	if err := s.resolve(envs); err != nil {
		return Spec{}, err
	}
	return s, nil
}

// Variable is the declared variable name.
func (s Spec) Variable(name string) (DeclaredVariable, bool) {
	i := slices.IndexFunc(s.Variables, func(v DeclaredVariable) bool { return v.Name == name })
	if i < 0 {
		return DeclaredVariable{}, false
	}
	return s.Variables[i], true
}

// HTTPTargets are the targets (<container>:<port>) of the http entries: the
// ones an ingress hostname may point at.
func (s Spec) HTTPTargets() map[string]bool {
	out := map[string]bool{}
	for _, e := range s.Expose {
		if e.Protocol == ProtocolHTTP {
			out[e.Target()] = true
		}
	}
	return out
}

// read merges one file's x-winterflow block into s.
func (s *Spec) read(x *yaml.Node) error {
	for i := 0; i+1 < len(x.Content); i += 2 {
		key, val := x.Content[i].Value, x.Content[i+1]
		var err error
		switch key {
		case "expose":
			err = s.readExpose(val)
		case "variables":
			err = s.readVariables(val)
		case "overrides":
			err = s.readOverrides(val)
		case "files":
			err = s.readFiles(val)
		case "backups":
			// recipe.ParseComposeNodes read it.
		default:
			err = invalid("x-winterflow."+key, "unknown key: use expose, variables, overrides, files or backups")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Spec) readExpose(list *yaml.Node) error {
	if list.Tag == "!!null" {
		return nil
	}
	if list.Kind != yaml.SequenceNode {
		return invalid("x-winterflow.expose", "must be a list of {container, port, protocol}")
	}
	seen := map[string]bool{}
	for i, item := range list.Content {
		field := fmt.Sprintf("x-winterflow.expose[%d]", i)
		if err := checkKeys(item, exposeKeys, field); err != nil {
			return err
		}
		var raw struct {
			Container string `yaml:"container"`
			Port      int    `yaml:"port"`
			Protocol  string `yaml:"protocol"`
		}
		if err := item.Decode(&raw); err != nil {
			return invalid(field, "%s", yamlError(err))
		}
		e := Expose{Container: raw.Container, Port: raw.Port, Protocol: raw.Protocol}
		if e.Protocol == "" {
			e.Protocol = ProtocolHTTP
		}
		if _, ok := s.Compose.Services[e.Container]; !ok {
			return invalid(field+".container", "%q is not a service of this compose file", e.Container)
		}
		if e.Port < 1 || e.Port > 65535 {
			return invalid(field+".port", "%d is not a port from 1 to 65535", e.Port)
		}
		if e.Protocol != ProtocolHTTP && e.Protocol != ProtocolTCP && e.Protocol != ProtocolUDP {
			return invalid(field+".protocol", "%q is not http, tcp or udp", e.Protocol)
		}
		if seen[e.ID()] {
			return invalid(field, "%s is listed twice", e.ID())
		}
		seen[e.ID()] = true
		if !slices.Contains(s.Expose, e) { // an earlier file's entry stays once
			s.Expose = append(s.Expose, e)
		}
	}
	return nil
}

func (s *Spec) readVariables(m *yaml.Node) error {
	if m.Tag == "!!null" {
		return nil
	}
	if m.Kind != yaml.MappingNode {
		return invalid("x-winterflow.variables", "must be a mapping of NAME: {required, encrypted, pattern, description}")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		name, def := m.Content[i].Value, m.Content[i+1]
		field := "x-winterflow.variables." + name
		if !nameRE.MatchString(name) {
			return invalid(field, "a name is upper-case letters, digits and _, starting with a letter")
		}
		if strings.HasPrefix(name, "WF_") || name == "COMPOSE_PROJECT_NAME" {
			return invalid(field, "%s is reserved", name)
		}
		v := DeclaredVariable{Name: name}
		if def.Tag != "!!null" {
			if err := checkKeys(def, variableKeys, field); err != nil {
				return err
			}
			var raw struct {
				Required    bool   `yaml:"required"`
				Encrypted   bool   `yaml:"encrypted"`
				Pattern     string `yaml:"pattern"`
				Description string `yaml:"description"`
			}
			if err := def.Decode(&raw); err != nil {
				return invalid(field, "%s", yamlError(err))
			}
			v.Required, v.Encrypted, v.Pattern, v.Description = raw.Required, raw.Encrypted, raw.Pattern, raw.Description
		}
		if len(v.Pattern) > MaxPattern {
			return invalid(field+".pattern", "at most %d bytes", MaxPattern)
		}
		if _, err := compilePattern(v.Pattern); err != nil {
			return invalid(field+".pattern", "%v", err)
		}
		if len(v.Description) > MaxDescription {
			return invalid(field+".description", "at most %d bytes", MaxDescription)
		}
		if j := slices.IndexFunc(s.Variables, func(o DeclaredVariable) bool { return o.Name == name }); j >= 0 {
			s.Variables[j] = v
		} else {
			s.Variables = append(s.Variables, v)
		}
	}
	return nil
}

func (s *Spec) readOverrides(m *yaml.Node) error {
	if m.Tag == "!!null" {
		return nil
	}
	if m.Kind != yaml.MappingNode {
		return invalid("x-winterflow.overrides", "must be a mapping with services")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if key := m.Content[i].Value; key != "services" {
			return invalid("x-winterflow.overrides."+key, "only services can be overridden")
		}
		svcs := m.Content[i+1]
		if svcs.Tag == "!!null" {
			continue
		}
		if svcs.Kind != yaml.MappingNode {
			return invalid("x-winterflow.overrides.services", "must be a mapping of service names")
		}
		for j := 0; j+1 < len(svcs.Content); j += 2 {
			name, body := svcs.Content[j].Value, svcs.Content[j+1]
			field := "x-winterflow.overrides.services." + name
			if _, ok := s.Compose.Services[name]; !ok {
				return invalid(field, "%q is not a service of this compose file", name)
			}
			if body.Kind != yaml.MappingNode {
				return invalid(field, "must be a mapping of the service's keys")
			}
			merged := mapping()
			if prev := s.Overrides[name]; prev != nil {
				merged.Content = append(merged.Content, prev.Content...)
			}
			for k := 0; k+1 < len(body.Content); k += 2 {
				setKey(merged, body.Content[k].Value, body.Content[k+1])
			}
			s.Overrides[name] = merged
		}
	}
	return nil
}

func (s *Spec) readFiles(list *yaml.Node) error {
	if list.Tag == "!!null" {
		return nil
	}
	var files []string
	if list.Kind != yaml.SequenceNode || list.Decode(&files) != nil {
		return invalid("x-winterflow.files", "must be a list of paths relative to the compose file")
	}
	for i, f := range files {
		if f == "" || len(f) > maxPath || strings.HasPrefix(f, "/") || path.Clean(f) != f ||
			f == "." || f == ".." || strings.HasPrefix(f, "../") || hasControl(f) {
			return invalid(fmt.Sprintf("x-winterflow.files[%d]", i), "%q is not a path relative to the compose file, like config.yml", f)
		}
		if !slices.Contains(s.Files, f) {
			s.Files = append(s.Files, f)
		}
	}
	return nil
}

// checkVolumes applies the volume-name rule to one file's top-level
// volumes: one that is not external and has a literal name: would be
// shared by every install of the file.
func (s *Spec) checkVolumes(root *yaml.Node, mode Mode) error {
	vols := value(root, "volumes")
	if vols == nil || vols.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(vols.Content); i += 2 {
		key, def := vols.Content[i].Value, vols.Content[i+1]
		name := value(def, "name")
		if name == nil || name.Kind != yaml.ScalarNode || name.Value == "" || strings.Contains(name.Value, "$") {
			continue
		}
		if ext := value(def, "external"); ext != nil && (ext.Value == "true" || ext.Kind == yaml.MappingNode) {
			continue
		}
		msg := fmt.Sprintf("volumes.%s: the name %q would be shared by every install of this compose file: drop name: (each app gets its own volume) or mark it external: true to share it on purpose", key, name.Value)
		if mode == ModeTemplate {
			return fmt.Errorf("%w: %s", ErrInvalid, msg)
		}
		s.Warnings = append(s.Warnings, msg)
	}
	return nil
}

// envKey is one entry of a service's environment.
type envKey struct {
	service string
	name    string
	literal *string // nil: a bare list NAME, a null or a non-scalar value
	comment string  // the YAML comment right above it
}

// envKeys lists the environment entries of every service of one file, in
// document order, map and list forms alike.
func envKeys(root *yaml.Node) []envKey {
	svcs := value(root, "services")
	if svcs == nil || svcs.Kind != yaml.MappingNode {
		return nil
	}
	var out []envKey
	for i := 0; i+1 < len(svcs.Content); i += 2 {
		svc := svcs.Content[i].Value
		env := value(svcs.Content[i+1], "environment")
		if env == nil {
			continue
		}
		switch env.Kind {
		case yaml.MappingNode:
			for j := 0; j+1 < len(env.Content); j += 2 {
				k, v := env.Content[j], env.Content[j+1]
				e := envKey{service: svc, name: k.Value, comment: comment(k.HeadComment)}
				if v.Kind == yaml.ScalarNode && v.Tag != "!!null" {
					lit := v.Value
					e.literal = &lit
				}
				out = append(out, e)
			}
		case yaml.SequenceNode:
			for _, item := range env.Content {
				if item.Kind != yaml.ScalarNode {
					continue
				}
				name, lit, ok := strings.Cut(item.Value, "=")
				e := envKey{service: svc, name: name, comment: comment(item.HeadComment)}
				if ok {
					e.literal = &lit
				}
				out = append(out, e)
			}
		}
	}
	return out
}

// resolve fills each variable's services, default and description from the
// environment entries, then checks the rules that need them.
func (s *Spec) resolve(envs []envKey) error {
	used := s.overrideRefs()
	for i := range s.Variables {
		v := &s.Variables[i]
		v.Services = []string{}
		for _, e := range envs {
			if e.name != v.Name || slices.Contains(v.Services, e.service) {
				continue
			}
			if len(v.Services) == 0 {
				if !v.Encrypted && e.literal != nil {
					lit := *e.literal
					v.Default = &lit
				}
				if v.Description == "" {
					v.Description = truncate(e.comment, MaxDescription)
				}
			}
			v.Services = append(v.Services, e.service)
		}
		field := "x-winterflow.variables." + v.Name
		if v.Default != nil && !matches(*v, *v.Default) {
			return invalid(field+".pattern", "the compose file's own value %q does not match %s", *v.Default, v.Pattern)
		}
		if len(v.Services) == 0 && !used[v.Name] {
			s.Warnings = append(s.Warnings, field+": no service environment and no override uses it")
		}
	}
	return nil
}

// overrideRefs are the variables the overrides interpolate.
func (s *Spec) overrideRefs() map[string]bool {
	refs := map[string]bool{}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			varref.References([]byte(n.Value), refs)
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	for _, n := range s.Overrides {
		walk(n)
	}
	return refs
}

// compilePattern compiles a pattern to match the whole value ("" matches
// anything).
func compilePattern(p string) (*regexp.Regexp, error) {
	return regexp.Compile(`^(?:` + p + `)$`)
}

// matches reports whether val satisfies v's pattern.
func matches(v DeclaredVariable, val string) bool {
	if v.Pattern == "" {
		return true
	}
	re, err := compilePattern(v.Pattern)
	return err == nil && re.MatchString(val)
}

// rootMapping is a decoded file's top-level mapping (nil: empty, not YAML,
// or not a mapping).
func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	if m := doc.Content[0]; m.Kind == yaml.MappingNode {
		return m
	}
	return nil
}

// value is the value of key in the mapping m (nil when absent).
func value(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// checkKeys refuses keys outside allowed in the mapping m.
func checkKeys(m *yaml.Node, allowed map[string]bool, field string) error {
	if m.Kind != yaml.MappingNode {
		return invalid(field, "must be a mapping")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i].Value; !allowed[k] {
			return invalid(field+"."+k, "unknown key")
		}
	}
	return nil
}

// yamlError drops the "yaml: unmarshal errors:" preamble.
func yamlError(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: unmarshal errors:")
	return strings.Join(strings.Fields(msg), " ")
}

// comment is a head comment without its # marks, lines joined by spaces.
func comment(c string) string {
	var parts []string
	for _, line := range strings.Split(c, "\n") {
		if line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")); line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " ")
}

// truncate cuts s to at most n bytes on a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// mapping is a new empty mapping node.
func mapping() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }

// scalar is a new string node.
func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// setKey sets key in the mapping m to val, replacing an existing entry.
func setKey(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, scalar(key), val)
}
