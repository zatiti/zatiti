package packaging

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const macPkgComponentID = "com.zatiti.installer"

// macPkgDistributionXML is a producer input, not a signed package. Final
// productbuild output is inspected again because that tool rewrites XML.
func macPkgDistributionXML(version, arch string) ([]byte, error) {
	if !versionPattern.MatchString(version) || (arch != "amd64" && arch != "arm64") {
		return nil, errf(CodeInvalidInput, "Mac package distribution target is invalid")
	}
	host := "x86_64"
	if arch == "arm64" {
		host = "arm64"
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="2">
<title>Zatiti</title>
<options customize="never" require-scripts="false" hostArchitectures="%s"/>
<domains enable_anywhere="false" enable_currentUserHome="true" enable_localSystem="false"/>
<choices-outline><line choice="zatiti"/></choices-outline>
<choice id="zatiti" title="Zatiti"><pkg-ref id="%s"/></choice>
<pkg-ref id="%s" version="%s">#component.pkg</pkg-ref>
</installer-gui-script>
`, host, macPkgComponentID, macPkgComponentID, version)), nil
}

type macXMLNode struct {
	name     string
	attrs    map[string]string
	text     string
	children []*macXMLNode
}

func parseMacPkgXML(raw []byte) (*macXMLNode, error) {
	d := xml.NewDecoder(strings.NewReader(string(raw)))
	var root *macXMLNode
	var stack []*macXMLNode
	nodes := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errf(CodeVerificationFailed, "Mac package XML is invalid")
		}
		switch v := tok.(type) {
		case xml.StartElement:
			if v.Name.Space != "" || len(stack) >= 8 || nodes >= 64 {
				return nil, errf(CodeVerificationFailed, "Mac package XML has unsupported shape")
			}
			n := &macXMLNode{name: v.Name.Local, attrs: make(map[string]string, len(v.Attr))}
			for _, a := range v.Attr {
				if a.Name.Space != "" {
					return nil, errf(CodeVerificationFailed, "Mac package XML namespace is unsupported")
				}
				if _, exists := n.attrs[a.Name.Local]; exists {
					return nil, errf(CodeVerificationFailed, "Mac package XML attribute is duplicated")
				}
				n.attrs[a.Name.Local] = a.Value
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, errf(CodeVerificationFailed, "Mac package XML has multiple roots")
				}
				root = n
			} else {
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
			}
			stack = append(stack, n)
			nodes++
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errf(CodeVerificationFailed, "Mac package XML closes unexpectedly")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(v)) != "" {
					return nil, errf(CodeVerificationFailed, "Mac package XML has trailing text")
				}
				continue
			}
			stack[len(stack)-1].text += string(v)
		case xml.ProcInst:
			if v.Target != "xml" || root != nil {
				return nil, errf(CodeVerificationFailed, "Mac package XML instruction is unsupported")
			}
		default:
			return nil, errf(CodeVerificationFailed, "Mac package XML directive is unsupported")
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, errf(CodeVerificationFailed, "Mac package XML is incomplete")
	}
	return root, nil
}

func (n *macXMLNode) hasAttrs(want map[string]string) bool {
	if len(n.attrs) != len(want) {
		return false
	}
	for k, v := range want {
		if n.attrs[k] != v {
			return false
		}
	}
	return true
}
func (n *macXMLNode) empty() bool { return strings.TrimSpace(n.text) == "" && len(n.children) == 0 }

func inspectMacPkgMetadata(x *macPkgXAR, version, arch string) error {
	distribution, err := x.readSmallMember("Distribution")
	if err != nil {
		return err
	}
	if err := verifyMacPkgDistribution(distribution, version, arch); err != nil {
		return err
	}
	component, err := x.readSmallMember("component.pkg/PackageInfo")
	if err != nil {
		return err
	}
	return verifyMacPkgComponentInfo(component, version)
}

func verifyMacPkgDistribution(raw []byte, version, arch string) error {
	n, err := parseMacPkgXML(raw)
	if err != nil {
		return err
	}
	if n.name != "installer-gui-script" || !n.hasAttrs(map[string]string{"minSpecVersion": "2"}) || strings.TrimSpace(n.text) != "" || len(n.children) != 7 {
		return errf(CodeVerificationFailed, "Mac package Distribution shape is unsupported")
	}
	byName := make(map[string][]*macXMLNode)
	for _, c := range n.children {
		byName[c.name] = append(byName[c.name], c)
	}
	if len(byName) != 6 || len(byName["title"]) != 1 || len(byName["options"]) != 1 || len(byName["domains"]) != 1 || len(byName["choices-outline"]) != 1 || len(byName["choice"]) != 1 || len(byName["pkg-ref"]) != 2 {
		return errf(CodeVerificationFailed, "Mac package Distribution has extra choices or elements")
	}
	title := byName["title"][0]
	if len(title.attrs) != 0 || len(title.children) != 0 || strings.TrimSpace(title.text) != "Zatiti" {
		return errf(CodeVerificationFailed, "Mac package title differs")
	}
	host := "x86_64"
	if arch == "arm64" {
		host = "arm64"
	}
	options := byName["options"][0]
	if !options.hasAttrs(map[string]string{"customize": "never", "require-scripts": "false", "hostArchitectures": host}) || !options.empty() {
		return errf(CodeVerificationFailed, "Mac package options allow unsupported behavior")
	}
	domains := byName["domains"][0]
	if !domains.hasAttrs(map[string]string{"enable_anywhere": "false", "enable_currentUserHome": "true", "enable_localSystem": "false"}) || !domains.empty() {
		return errf(CodeVerificationFailed, "Mac package does not restrict installation to current user home")
	}
	outline := byName["choices-outline"][0]
	if len(outline.attrs) != 0 || strings.TrimSpace(outline.text) != "" || len(outline.children) != 1 || outline.children[0].name != "line" || !outline.children[0].hasAttrs(map[string]string{"choice": "zatiti"}) || !outline.children[0].empty() {
		return errf(CodeVerificationFailed, "Mac package choice outline is unsupported")
	}
	choice := byName["choice"][0]
	if !choice.hasAttrs(map[string]string{"id": "zatiti", "title": "Zatiti"}) || strings.TrimSpace(choice.text) != "" || len(choice.children) != 1 || choice.children[0].name != "pkg-ref" || !choice.children[0].hasAttrs(map[string]string{"id": macPkgComponentID}) || !choice.children[0].empty() {
		return errf(CodeVerificationFailed, "Mac package choice is unsupported")
	}
	full, stub := byName["pkg-ref"][0], byName["pkg-ref"][1]
	if strings.TrimSpace(full.text) != "#component.pkg" {
		full, stub = stub, full
	}
	if strings.TrimSpace(full.text) != "#component.pkg" || len(full.children) != 0 || !hasProductPkgRefAttrs(full.attrs, version) {
		return errf(CodeVerificationFailed, "Mac package component reference is unsafe")
	}
	if !stub.hasAttrs(map[string]string{"id": macPkgComponentID}) || strings.TrimSpace(stub.text) != "" || len(stub.children) != 1 || stub.children[0].name != "bundle-version" || len(stub.children[0].attrs) != 0 || !stub.children[0].empty() {
		return errf(CodeVerificationFailed, "Mac package component supplement is unsafe")
	}
	return nil
}

func hasProductPkgRefAttrs(attrs map[string]string, version string) bool {
	if len(attrs) != 4 || attrs["id"] != macPkgComponentID || attrs["version"] != version || attrs["updateKBytes"] != "0" {
		return false
	}
	_, err := strconv.ParseUint(attrs["installKBytes"], 10, 64)
	return err == nil
}

func verifyMacPkgComponentInfo(raw []byte, version string) error {
	n, err := parseMacPkgXML(raw)
	if err != nil {
		return err
	}
	if n.name != "pkg-info" || strings.TrimSpace(n.text) != "" || len(n.children) != 7 || len(n.attrs) != 9 || n.attrs["overwrite-permissions"] != "true" || n.attrs["relocatable"] != "false" || n.attrs["identifier"] != macPkgComponentID || n.attrs["postinstall-action"] != "none" || n.attrs["version"] != version || n.attrs["format-version"] != "2" || n.attrs["generator-version"] == "" || n.attrs["install-location"] != "/" || (n.attrs["auth"] != "root" && n.attrs["auth"] != "none") {
		return errf(CodeVerificationFailed, "Mac package component metadata is unsupported")
	}
	want := map[string]bool{"payload": true, "bundle-version": true, "upgrade-bundle": true, "update-bundle": true, "atomic-update-bundle": true, "strict-identifier": true, "relocate": true}
	for _, c := range n.children {
		if !want[c.name] || !c.empty() {
			return errf(CodeVerificationFailed, "Mac package component contains scripts or unsupported metadata")
		}
		delete(want, c.name)
		if c.name == "payload" {
			if len(c.attrs) != 2 || c.attrs["numberOfFiles"] != "10" {
				return errf(CodeVerificationFailed, "Mac package component file count differs")
			}
			if _, err := strconv.ParseUint(c.attrs["installKBytes"], 10, 64); err != nil {
				return errf(CodeVerificationFailed, "Mac package component size metadata is invalid")
			}
		} else if len(c.attrs) != 0 {
			return errf(CodeVerificationFailed, "Mac package component has unsupported attributes")
		}
	}
	if len(want) != 0 {
		return errf(CodeVerificationFailed, "Mac package component metadata is incomplete")
	}
	return nil
}
