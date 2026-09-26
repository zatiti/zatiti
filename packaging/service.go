package packaging

import (
	"bytes"
	"embed"
	"encoding/xml"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

//go:embed templates/launchd/agent.plist.tmpl templates/systemd/user.service.tmpl
var templateFS embed.FS

// Service managers.
const (
	ManagerLaunchd     = "launchd"
	ManagerSystemdUser = "systemd_user"
)

// Service roles.
const (
	RoleController = "controller"
	RoleSerenity   = "serenity"
)

// Default service labels. They follow the keychain service namespace the
// platform package already uses.
const (
	ControllerLabel = "com.zatiti.controller"
	SerenityLabel   = "com.zatiti.serenity"

	defaultStopTimeoutSeconds = 30
	unitFileMode              = fs.FileMode(0o644)

	// The controller's private socket defaults to <state dir>/zatiti.sock
	// and cmd/zatiti refuses a socket path of maxSocketPathBytes or more
	// before it assembles anything (a Unix-domain sun_path bound). The
	// launcher refuses the same path up front so an installed controller
	// never starts into that refusal.
	socketFileName     = "zatiti.sock"
	maxSocketPathBytes = 104
	socketEnvName      = "ZATITI_SOCKET"
	socketFlagName     = "socket"
)

// ServiceSpec describes one always-on user service. The controller service
// runs `<executable> serve`; any further arguments come from the caller,
// because the entrypoint's flags are not part of the frozen contract. A
// Serenity service takes its executable and whole argument list from the
// qualified pin; this package supplies no default for either.
type ServiceSpec struct {
	Manager     string
	Role        string
	Label       string
	Description string
	// Executable is the absolute path of the binary to run.
	Executable string
	// Arguments follow the executable. For the controller role the first
	// argument must be "serve".
	Arguments []string
	// Owns is the absolute directory this service is the single writer of:
	// the installation state directory for the controller, one brain
	// directory for a Serenity service.
	Owns             string
	WorkingDirectory string
	// Environment is for non-secret settings only. Secrets reach the
	// controller through the secret store, never through a launcher.
	Environment map[string]string
	// LogDirectory, when set, receives <label>.log with owner-only
	// permissions. Empty leaves output with the service manager.
	LogDirectory string
}

// Unit is a rendered launcher file.
type Unit struct {
	Label    string
	FileName string
	Content  []byte
	Mode     fs.FileMode
}

var (
	launchdLabelPattern = regexp.MustCompile(`^[A-Za-z0-9]+(\.[A-Za-z0-9-]+)+$`)
	descriptionPattern  = regexp.MustCompile(`^[A-Za-z0-9 ._()-]{1,80}$`)
	envNamePattern      = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	systemdPathPattern  = regexp.MustCompile(`^/[A-Za-z0-9/_.+@-]*$`)
	secretNamePattern   = regexp.MustCompile(`(?i)(secret|token|passw(or)?d|api[_-]?key|private[_-]?key|bearer)`)
	referenceSuffix     = regexp.MustCompile(`(?i)[_-](ref|file|path|dir)$`)
)

type envPair struct{ Name, Value string }

type unitData struct {
	Label              string
	Description        string
	Executable         string
	Arguments          []string
	WorkingDirectory   string
	Environment        []envPair
	LogFile            string
	StopTimeoutSeconds int
}

// Render validates spec and renders its launcher. The launcher keeps the
// service alive on its own (RunAtLoad and KeepAlive, or Restart=always under
// default.target) and names no other unit, so no client, including the
// desktop client, can stop it by exiting.
func Render(spec ServiceSpec) (Unit, error) {
	if err := spec.validate(); err != nil {
		return Unit{}, err
	}
	data := unitData{
		Label:              spec.Label,
		Description:        spec.Description,
		Executable:         spec.Executable,
		Arguments:          spec.Arguments,
		WorkingDirectory:   spec.WorkingDirectory,
		StopTimeoutSeconds: defaultStopTimeoutSeconds,
	}
	for name, value := range spec.Environment {
		data.Environment = append(data.Environment, envPair{Name: name, Value: value})
	}
	sort.Slice(data.Environment, func(i, j int) bool { return data.Environment[i].Name < data.Environment[j].Name })
	if spec.LogDirectory != "" {
		data.LogFile = filepath.Join(spec.LogDirectory, spec.Label+".log")
	}
	name, fileName := "templates/launchd/agent.plist.tmpl", spec.Label+".plist"
	if spec.Manager == ManagerSystemdUser {
		name, fileName = "templates/systemd/user.service.tmpl", spec.Label+".service"
	}
	tmpl, err := template.New(filepath.Base(name)).Funcs(template.FuncMap{
		"xml":      xmlEscape,
		"quote":    systemdQuoteArg,
		"quoteenv": systemdQuoteEnv,
	}).ParseFS(templateFS, name)
	if err != nil {
		return Unit{}, errWrap(CodeInternalError, "the launcher template could not be parsed", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return Unit{}, errWrap(CodeInternalError, "the launcher template could not be rendered", err)
	}
	return Unit{Label: spec.Label, FileName: fileName, Content: buf.Bytes(), Mode: unitFileMode}, nil
}

func (s ServiceSpec) validate() error {
	if s.Manager != ManagerLaunchd && s.Manager != ManagerSystemdUser {
		return errf(CodeCapabilityUnsupported, "service manager must be %s or %s", ManagerLaunchd, ManagerSystemdUser)
	}
	if s.Role != RoleController && s.Role != RoleSerenity {
		return errf(CodeInvalidInput, "service role must be %s or %s", RoleController, RoleSerenity)
	}
	if !launchdLabelPattern.MatchString(s.Label) || len(s.Label) > 100 {
		return errf(CodeInvalidInput, "service label must be a reverse-DNS name")
	}
	if !descriptionPattern.MatchString(s.Description) {
		return errf(CodeInvalidInput, "service description must be 1 to 80 plain characters")
	}
	paths := []struct{ what, path string }{
		{"executable", s.Executable},
		{"owned directory", s.Owns},
		{"working directory", s.WorkingDirectory},
		{"log directory", s.LogDirectory},
	}
	for i, p := range paths {
		if i >= 2 && p.path == "" {
			continue // optional
		}
		if !filepath.IsAbs(p.path) || filepath.Clean(p.path) != p.path || hasControl(p.path) {
			return errf(CodeInvalidInput, "the service %s must be a clean absolute path", p.what)
		}
		// systemd expands specifiers and splits on spaces in path
		// settings; refuse what cannot be written without escaping. The
		// owned directory is never rendered into a unit.
		if s.Manager == ManagerSystemdUser && i != 1 && !systemdPathPattern.MatchString(p.path) {
			return errf(CodeInvalidInput, "the service %s contains a character a systemd unit cannot carry safely", p.what)
		}
	}
	if s.Role == RoleController {
		if len(s.Arguments) == 0 || s.Arguments[0] != "serve" {
			return errf(CodeInvalidInput, "the controller service must run the serve command")
		}
		if len(s.socketPath()) >= maxSocketPathBytes {
			return errf(CodeInvalidInput, "the controller socket path would be %d bytes or longer and the controller refuses it; set %s or --%s to a shorter path", maxSocketPathBytes, socketEnvName, socketFlagName)
		}
	}
	fixedMaster := hasFixedMacMasterSelector(s.Arguments)
	if fixedMaster {
		if s.Manager != ManagerLaunchd || s.Role != RoleController || !strings.HasSuffix(s.Owns, string(filepath.Separator)+filepath.Join("Library", "Application Support", "zatiti")) || !hasFixedMacCredentialBackend(s.Arguments) {
			return errf(CodeInvalidInput, "the fixed Mac master selector requires a Keychain controller at the default state directory")
		}
	}
	for i, arg := range s.Arguments {
		if arg == "" || hasControl(arg) || len(arg) > 4096 {
			return errf(CodeInvalidInput, "service arguments must be non-empty and free of control characters")
		}
		if (arg == "--master-key" || strings.HasPrefix(arg, "--master-key=")) && isDefaultMacStateDir(s.Owns) && !fixedMaster {
			return errf(CodeInvalidInput, "the service master-key selector is not the fixed Mac Keychain selector")
		}
		if fixedMaster && (arg == "--master-key-ref" || strings.HasPrefix(arg, "--master-key-ref=")) {
			return errf(CodeInvalidInput, "the fixed Mac master selector cannot be overridden")
		}
		if strings.HasPrefix(arg, "-") {
			flag, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			allowedFixedMaster := fixedMaster && flag == "master-key" && arg == "--master-key" && i+1 < len(s.Arguments) && s.Arguments[i+1] == "secret:master"
			if carriesSecret(flag) && !allowedFixedMaster {
				return errf(CodeInvalidInput, "service arguments must not carry secret material; provision it through the secret store")
			}
		}
	}
	for name, value := range s.Environment {
		if !envNamePattern.MatchString(name) {
			return errf(CodeInvalidInput, "service environment names must be uppercase identifiers")
		}
		if carriesSecret(name) {
			return errf(CodeInvalidInput, "the service environment must not carry secret material; provision it through the secret store")
		}
		if hasControl(value) || len(value) > 4096 {
			return errf(CodeInvalidInput, "service environment values must be free of control characters")
		}
	}
	return nil
}

// The only secret-named argument allowed in a Mac launcher is the fixed,
// nonsecret Keychain item selector. Duplicate or alternate selectors fail.
func hasFixedMacMasterSelector(args []string) bool {
	count := 0
	for i, arg := range args {
		if arg == "--master-key" && i+1 < len(args) && args[i+1] == "secret:master" {
			count++
		} else if arg == "--master-key" || strings.HasPrefix(arg, "--master-key=") {
			return false
		}
	}
	return count == 1
}

func isDefaultMacStateDir(path string) bool {
	return strings.HasSuffix(path, string(filepath.Separator)+filepath.Join("Library", "Application Support", "zatiti"))
}

func hasFixedMacCredentialBackend(args []string) bool {
	count := 0
	for i, arg := range args {
		if arg == "--credential-backend" && i+1 < len(args) && args[i+1] == "keychain" {
			count++
		} else if arg == "--credential-backend" || strings.HasPrefix(arg, "--credential-backend=") {
			return false
		}
	}
	return count == 1
}

// socketPath resolves the socket the controller would listen on: an
// explicit --socket argument, else the ZATITI_SOCKET environment entry, else
// zatiti.sock inside the owned state directory. The flag wins over the
// environment as in cmd/zatiti.
func (s ServiceSpec) socketPath() string {
	for i, arg := range s.Arguments {
		if value, ok := strings.CutPrefix(arg, "--"+socketFlagName+"="); ok {
			return value
		}
		if arg == "--"+socketFlagName && i+1 < len(s.Arguments) {
			return s.Arguments[i+1]
		}
	}
	if value, ok := s.Environment[socketEnvName]; ok {
		return value
	}
	return filepath.Join(s.Owns, socketFileName)
}

// carriesSecret is a name-based guard against the obvious mistake of putting
// a credential in a launcher. It is not secret detection: a name that points
// at a reference, file or directory is allowed, and a secret under an
// innocuous name is not caught.
func carriesSecret(name string) bool {
	return secretNamePattern.MatchString(name) && !referenceSuffix.MatchString(name)
}

// ValidateServices checks a set of services that will run side by side: one
// controller at most, unique labels, one manager, and no two services
// writing the same directory tree. That is the packaging half of "one writer
// owner per brain and one controller state lock"; the running processes
// enforce the other half with their own locks.
func ValidateServices(specs []ServiceSpec) error {
	labels := map[string]bool{}
	controllers := 0
	for i, s := range specs {
		if err := s.validate(); err != nil {
			return err
		}
		if s.Manager != specs[0].Manager {
			return errf(CodeInvalidInput, "all services of one installation use the same service manager")
		}
		if labels[s.Label] {
			return errf(CodeConflict, "service label %s is used twice", s.Label)
		}
		labels[s.Label] = true
		if s.Role == RoleController {
			controllers++
		}
		for _, other := range specs[:i] {
			if pathWithin(s.Owns, other.Owns) || pathWithin(other.Owns, s.Owns) {
				return errf(CodeConflict, "services %s and %s would write the same directory tree", other.Label, s.Label)
			}
		}
	}
	if controllers > 1 {
		return errf(CodeConflict, "an installation runs exactly one controller")
	}
	return nil
}

// pathWithin reports whether child equals parent or lies beneath it. Both
// must be clean absolute paths.
func pathWithin(child, parent string) bool {
	if child == parent {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func xmlEscape(s string) (string, error) {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var systemdEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`)

// systemdQuoteArg quotes one ExecStart argument: C-style escapes inside
// double quotes, "%%" for a literal percent and "$$" for a literal dollar.
func systemdQuoteArg(s string) string {
	return `"` + strings.ReplaceAll(systemdEscaper.Replace(s), `$`, `$$`) + `"`
}

// systemdQuoteEnv quotes one Environment= assignment. Environment values are
// not subject to dollar expansion.
func systemdQuoteEnv(name, value string) string {
	return `"` + name + `=` + systemdEscaper.Replace(value) + `"`
}
