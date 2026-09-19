package packaging

import (
	"bytes"
	"encoding/xml"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// hostile holds the characters each launcher format treats specially.
var hostile = []string{`a&b<c>"d"'e'`, `50% $HOME \n "quoted" back\slash`, `--config-path=/opt/my dir/zatiti.json`}

// plistStrings parses a rendered plist with a real XML parser and returns
// the text of every <key> and <string> element in document order.
func plistStrings(t *testing.T, content []byte) []string {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(content))
	var out []string
	var capture bool
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("rendered plist is not well-formed XML: %v", err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			capture = el.Name.Local == "key" || el.Name.Local == "string"
			if !capture {
				out = append(out, "<"+el.Name.Local+">")
			}
			text.Reset()
		case xml.CharData:
			if capture {
				text.Write(el)
			}
		case xml.EndElement:
			if capture {
				out = append(out, text.String())
				capture = false
			}
		}
	}
}

// systemdSplit undoes the quoting rules of systemd.syntax for a line made
// only of double-quoted words, including the %% and $$ escapes.
func systemdSplit(t *testing.T, line string, dollars bool) []string {
	t.Helper()
	var words []string
	for line = strings.TrimSpace(line); line != ""; line = strings.TrimSpace(line) {
		if line[0] != '"' {
			t.Fatalf("unquoted word in %q", line)
		}
		var word strings.Builder
		i := 1
		for ; i < len(line) && line[i] != '"'; i++ {
			switch {
			case line[i] == '\\':
				i++
				word.WriteByte(line[i])
			case line[i] == '%', dollars && line[i] == '$':
				if i+1 >= len(line) || line[i+1] != line[i] {
					t.Fatalf("unescaped %q in %q", line[i], line)
				}
				word.WriteByte(line[i])
				i++
			default:
				word.WriteByte(line[i])
			}
		}
		words = append(words, word.String())
		line = line[i+1:]
	}
	return words
}

func lineWithPrefix(t *testing.T, content []byte, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	t.Fatalf("no line with prefix %q in:\n%s", prefix, content)
	return ""
}

func launchdSpec() ServiceSpec {
	return ServiceSpec{
		Manager: ManagerLaunchd, Role: RoleController, Label: ControllerLabel,
		Description: "Zatiti controller",
		Executable:  "/Users/operator/Library/Application Support/zatiti-dist/current/bin/zatiti",
		Arguments:   append([]string{"serve"}, hostile...),
		Owns:        "/Users/operator/zatiti-state",
		Environment: map[string]string{"ZATITI_LOG_LEVEL": hostile[0], "A_FIRST": "1"},
	}
}

func systemdSpec() ServiceSpec {
	s := launchdSpec()
	s.Manager = ManagerSystemdUser
	s.Executable = "/home/operator/.local/share/zatiti-dist/current/bin/zatiti"
	s.Owns = "/home/operator/zatiti-state"
	s.Environment = map[string]string{"ZATITI_LOG_LEVEL": hostile[1], "A_FIRST": "1"}
	return s
}

func TestRenderLaunchAgent(t *testing.T) {
	t.Parallel()
	spec := launchdSpec()
	spec.LogDirectory = "/Users/operator/Library/Logs/zatiti"
	u, err := Render(spec)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if u.FileName != "com.zatiti.controller.plist" || u.Mode != 0o644 {
		t.Fatalf("unit file is %s mode %o", u.FileName, u.Mode)
	}
	got := plistStrings(t, u.Content)
	want := []string{
		"<plist>", "<dict>",
		"Label", ControllerLabel,
		"ProgramArguments", "<array>", spec.Executable, "serve", hostile[0], hostile[1], hostile[2],
		"RunAtLoad", "<true>",
		"KeepAlive", "<true>",
		"ProcessType", "Background",
		"Umask", "<integer>",
		"ExitTimeOut", "<integer>",
		"EnvironmentVariables", "<dict>", "A_FIRST", "1", "ZATITI_LOG_LEVEL", hostile[0],
		"StandardOutPath", spec.LogDirectory + "/com.zatiti.controller.log",
		"StandardErrorPath", spec.LogDirectory + "/com.zatiti.controller.log",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plist structure:\n got %q\nwant %q", got, want)
	}
	// Umask 63 is octal 077: the log file launchd creates is owner-only.
	if !bytes.Contains(u.Content, []byte("<key>Umask</key>\n\t<integer>63</integer>")) {
		t.Fatalf("plist does not pin an owner-only umask:\n%s", u.Content)
	}
}

func TestRenderSystemdUnit(t *testing.T) {
	t.Parallel()
	spec := systemdSpec()
	u, err := Render(spec)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if u.FileName != "com.zatiti.controller.service" {
		t.Fatalf("unit file is %s", u.FileName)
	}
	exec := lineWithPrefix(t, u.Content, "ExecStart=")
	if !strings.HasPrefix(exec, spec.Executable+" ") {
		t.Fatalf("ExecStart does not start with the executable: %s", exec)
	}
	if got := systemdSplit(t, strings.TrimPrefix(exec, spec.Executable), true); !reflect.DeepEqual(got, spec.Arguments) {
		t.Fatalf("arguments do not survive systemd quoting:\n got %q\nwant %q", got, spec.Arguments)
	}
	envLines := 0
	for _, line := range strings.Split(string(u.Content), "\n") {
		if strings.HasPrefix(line, "Environment=") {
			envLines++
		}
	}
	if envLines != 2 {
		t.Fatalf("got %d Environment lines, want 2", envLines)
	}
	envLine := `"ZATITI` + lineWithPrefix(t, u.Content, `Environment="ZATITI`)
	if got := systemdSplit(t, envLine, false); !reflect.DeepEqual(got, []string{"ZATITI_LOG_LEVEL=" + hostile[1]}) {
		t.Fatalf("environment does not survive systemd quoting: %q", got)
	}
	for _, directive := range []string{"Restart=always", "UMask=0077", "NoNewPrivileges=yes", "TimeoutStopSec=30", "WantedBy=default.target", "Type=simple"} {
		if !strings.Contains(string(u.Content), "\n"+directive+"\n") {
			t.Fatalf("unit lacks %s:\n%s", directive, u.Content)
		}
	}
}

// The controller must outlive the desktop client: its launcher may not bind
// its lifetime to any other unit or mention the desktop at all.
func TestLaunchersAreIndependentOfTheDesktop(t *testing.T) {
	t.Parallel()
	for _, spec := range []ServiceSpec{launchdSpec(), systemdSpec()} {
		u, err := Render(spec)
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(u.Content))
		for _, banned := range []string{"desktop", "partof=", "bindsto=", "requires=", "after=", "stopwhenunneeded", "graphical-session", "limitloadtosessiontype", "launchonlyonce"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("%s launcher contains %q:\n%s", spec.Manager, banned, u.Content)
			}
		}
	}
}

func TestRenderRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		base   func() ServiceSpec
		mutate func(s *ServiceSpec)
		code   string
	}{
		{"unknown manager", launchdSpec, func(s *ServiceSpec) { s.Manager = "sysv" }, CodeCapabilityUnsupported},
		{"unknown role", launchdSpec, func(s *ServiceSpec) { s.Role = "desktop" }, CodeInvalidInput},
		{"label with path", launchdSpec, func(s *ServiceSpec) { s.Label = "../../evil" }, CodeInvalidInput},
		{"description with newline", systemdSpec, func(s *ServiceSpec) { s.Description = "x\n[Service]" }, CodeInvalidInput},
		{"relative executable", launchdSpec, func(s *ServiceSpec) { s.Executable = "bin/zatiti" }, CodeInvalidInput},
		{"controller not serving", launchdSpec, func(s *ServiceSpec) { s.Arguments = []string{"mcp", "serve"} }, CodeInvalidInput},
		{"controller without arguments", launchdSpec, func(s *ServiceSpec) { s.Arguments = nil }, CodeInvalidInput},
		{"newline in argument", systemdSpec, func(s *ServiceSpec) { s.Arguments = []string{"serve", "x\nExecStartPre=/bin/false"} }, CodeInvalidInput},
		{"token flag", launchdSpec, func(s *ServiceSpec) { s.Arguments = []string{"serve", "--owner-token=abc"} }, CodeInvalidInput},
		{"password flag as separate word", launchdSpec, func(s *ServiceSpec) { s.Arguments = []string{"serve", "--password", "abc"} }, CodeInvalidInput},
		{"api key environment", systemdSpec, func(s *ServiceSpec) { s.Environment = map[string]string{"OPENAI_API_KEY": "abc"} }, CodeInvalidInput},
		{"lowercase environment name", systemdSpec, func(s *ServiceSpec) { s.Environment = map[string]string{"path": "/bin"} }, CodeInvalidInput},
		{"systemd executable with space", systemdSpec, func(s *ServiceSpec) { s.Executable = "/opt/my dir/zatiti" }, CodeInvalidInput},
		{"systemd working directory with specifier", systemdSpec, func(s *ServiceSpec) { s.WorkingDirectory = "/opt/%h" }, CodeInvalidInput},
		{"owned directory missing", launchdSpec, func(s *ServiceSpec) { s.Owns = "" }, CodeInvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := tc.base()
			tc.mutate(&s)
			_, err := Render(s)
			wantCode(t, err, tc.code)
		})
	}
}

// cmd/zatiti refuses a socket path of 104 bytes or more. The launcher must
// refuse the same path before the controller is ever installed behind it.
func TestControllerSocketPathBound(t *testing.T) {
	t.Parallel()
	deep := "/Users/operator/Library/Application Support/" + strings.Repeat("deeply-nested/", 4) + "zatiti-state"
	if got := len(filepath.Join(deep, "zatiti.sock")); got < 104 {
		t.Fatalf("fixture path is only %d bytes", got)
	}
	long := launchdSpec()
	long.Owns = deep
	long.Arguments = []string{"serve", "--state-dir", deep}
	_, err := Render(long)
	perr := wantFault(t, err, CodeInvalidInput)
	if !strings.Contains(perr.Message, "ZATITI_SOCKET") {
		t.Fatalf("message does not name the remedy: %s", perr.Message)
	}

	viaFlag := long
	viaFlag.Arguments = []string{"serve", "--state-dir", deep, "--socket=/tmp/zatiti.sock"}
	if _, err := Render(viaFlag); err != nil {
		t.Fatalf("an explicit short --socket must be accepted: %v", err)
	}
	viaWord := long
	viaWord.Arguments = []string{"serve", "--socket", "/tmp/zatiti.sock"}
	if _, err := Render(viaWord); err != nil {
		t.Fatalf("an explicit short --socket word must be accepted: %v", err)
	}
	viaEnv := long
	viaEnv.Environment = map[string]string{"ZATITI_SOCKET": "/tmp/zatiti.sock"}
	if _, err := Render(viaEnv); err != nil {
		t.Fatalf("an explicit short ZATITI_SOCKET must be accepted: %v", err)
	}
	stillLong := long
	stillLong.Environment = map[string]string{"ZATITI_SOCKET": filepath.Join(deep, "other.sock")}
	_, err = Render(stillLong)
	wantCode(t, err, CodeInvalidInput)
	short := launchdSpec()
	if got := len(filepath.Join(short.Owns, "zatiti.sock")); got >= 104 {
		t.Fatalf("the default fixture socket is %d bytes", got)
	}
}

func TestReferencesAreNotMistakenForSecrets(t *testing.T) {
	t.Parallel()
	s := launchdSpec()
	s.Arguments = []string{"serve", "--master-key-ref=secret:master", "--credential-backend=keychain", "--token-file=/run/zatiti/owner"}
	s.Environment = map[string]string{"ZATITI_SECRET_DIR": "/var/lib/zatiti"}
	if _, err := Render(s); err != nil {
		t.Fatalf("Render: %v", err)
	}
}

func TestValidateServices(t *testing.T) {
	t.Parallel()
	controller := systemdSpec()
	brain := func(label, dir string) ServiceSpec {
		return ServiceSpec{
			Manager: ManagerSystemdUser, Role: RoleSerenity, Label: label, Description: "Serenity brain",
			Executable: "/home/operator/.local/share/zatiti-dist/current/serenity/serenity", Owns: dir,
		}
	}
	if err := ValidateServices([]ServiceSpec{controller, brain("com.zatiti.serenity.a", "/home/operator/brains/a"), brain("com.zatiti.serenity.b", "/home/operator/brains/b")}); err != nil {
		t.Fatalf("a controller and two brains with separate directories: %v", err)
	}
	second := controller
	second.Label = "com.zatiti.controller2"
	second.Owns = "/home/operator/other-state"
	mixed := brain("com.zatiti.serenity.a", "/home/operator/brains/a")
	mixed.Manager = ManagerLaunchd
	tests := []struct {
		name  string
		specs []ServiceSpec
		code  string
	}{
		{"two writers of one brain", []ServiceSpec{brain("com.zatiti.serenity.a", "/home/operator/brains/a"), brain("com.zatiti.serenity.b", "/home/operator/brains/a")}, CodeConflict},
		{"brain nested in controller state", []ServiceSpec{controller, brain("com.zatiti.serenity.a", controller.Owns+"/brain")}, CodeConflict},
		{"two controllers", []ServiceSpec{controller, second}, CodeConflict},
		{"duplicate label", []ServiceSpec{brain("com.zatiti.serenity.a", "/home/operator/brains/a"), brain("com.zatiti.serenity.a", "/home/operator/brains/b")}, CodeConflict},
		{"mixed managers", []ServiceSpec{controller, mixed}, CodeInvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantCode(t, ValidateServices(tc.specs), tc.code)
		})
	}
}
