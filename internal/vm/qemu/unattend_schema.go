package qemu

import (
	"encoding/xml"
	"fmt"
	"slices"
	"strings"
)

// Validation of answer files against the documented unattend schema.
//
// The authoritative unattend.xsd ships inside the Windows ADK (Windows-only),
// so this encodes the placement rules from the published component reference
// instead. That covers the failure mode that actually hurts: Windows Setup
// silently ignores an element nested under a component that does not define
// it, so a misplaced setting costs a full multi-hour install run to discover
// rather than failing loudly.

// unattendPasses are the seven configuration passes Windows Setup runs.
var unattendPasses = []string{
	"windowsPE", "offlineServicing", "generalize",
	"specialize", "auditSystem", "auditUser", "oobeSystem",
}

// passRule records which configuration passes a setting is valid in. The
// generated component table (unattend_components_gen.go) covers *where* each
// setting lives; the reference publishes valid passes only on the individual
// setting pages, so the ones we depend on are curated here.
type passRule struct {
	Passes []string
	Doc    string
}

// passRules is keyed "Component/Element" because the valid passes depend on
// both: DriverPaths is windowsPE-only under PnpCustomizationsWinPE, but
// offlineServicing/auditSystem under PnpCustomizationsNonWinPE, and
// RunSynchronous is windowsPE under Setup but specialize/auditUser under
// Deployment.
var passRules = map[string]passRule{
	"Microsoft-Windows-Setup/DiskConfiguration":                  {Passes: []string{"windowsPE"}, Doc: "microsoft-windows-setup-diskconfiguration"},
	"Microsoft-Windows-Setup/ImageInstall":                       {Passes: []string{"windowsPE"}, Doc: "microsoft-windows-setup-imageinstall"},
	"Microsoft-Windows-Setup/UserData":                           {Passes: []string{"windowsPE"}, Doc: "microsoft-windows-setup-userdata"},
	"Microsoft-Windows-Setup/RunSynchronous":                     {Passes: []string{"windowsPE"}, Doc: "microsoft-windows-setup-runsynchronous"},
	"Microsoft-Windows-PnpCustomizationsWinPE/DriverPaths":       {Passes: []string{"windowsPE"}, Doc: "microsoft-windows-pnpcustomizationswinpe-driverpaths"},
	"Microsoft-Windows-PnpCustomizationsNonWinPE/DriverPaths":    {Passes: []string{"offlineServicing", "auditSystem"}, Doc: "microsoft-windows-pnpcustomizationsnonwinpe-driverpaths"},
	"Microsoft-Windows-Deployment/RunSynchronous":                {Passes: []string{"specialize", "auditUser"}, Doc: "microsoft-windows-deployment-runsynchronous"},
	"Microsoft-Windows-Deployment/RunAsynchronous":               {Passes: []string{"specialize", "auditUser"}, Doc: "microsoft-windows-deployment-runasynchronous"},
	"Microsoft-Windows-Shell-Setup/OOBE":                         {Passes: []string{"oobeSystem"}, Doc: "microsoft-windows-shell-setup-oobe"},
	"Microsoft-Windows-Shell-Setup/UserAccounts":                 {Passes: []string{"oobeSystem", "auditSystem"}, Doc: "microsoft-windows-shell-setup-useraccounts"},
	"Microsoft-Windows-Shell-Setup/AutoLogon":                    {Passes: []string{"oobeSystem", "specialize", "auditSystem"}, Doc: "microsoft-windows-shell-setup-autologon"},
	"Microsoft-Windows-Shell-Setup/FirstLogonCommands":           {Passes: []string{"oobeSystem"}, Doc: "microsoft-windows-shell-setup-firstlogoncommands"},
	"Microsoft-Windows-International-Core-WinPE/SetupUILanguage": {Passes: []string{"windowsPE"}, Doc: "microsoft-windows-international-core-winpe-setupuilanguage"},
}

// bannedElements are settings that must never appear, with the reason.
var bannedElements = map[string]string{
	"LabConfig": "LabConfig is a registry key (HKLM\\SYSTEM\\Setup\\LabConfig), " +
		"not an unattend element — write it with reg add in a RunSynchronousCommand",
	// SkipMachineOOBE / SkipUserOOBE were banned here on Microsoft's advice
	// ("hide the individual screens instead"). They are deliberately NOT banned
	// any more: on ARM64 Windows 11 under QEMU/TCG the Hide*-only path installs
	// fine and then dies inside OOBE at the Zero Day Patch step (OOBEZDP),
	// because Hide* hides screens without skipping OOBE and ZDP is not
	// hideable. See TestGenerateAutounattendXML_SkipsOOBEEntirely for the
	// run-by-run evidence, and revisit if a NIC driver is ever injected.
}

const (
	unattendDocBase      = "https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/"
	unattendReferenceURL = unattendDocBase
)

// ValidateUnattend checks an answer file against the documented placement of
// the settings devcell relies on. It returns every problem found rather than
// stopping at the first, so one run surfaces all of them.
//
// Unknown elements are ignored: this validates the settings we depend on, and
// does not attempt to be a complete schema.
func ValidateUnattend(answerFile []byte) []error {
	var doc unattendDoc
	if err := xml.Unmarshal(answerFile, &doc); err != nil {
		return []error{fmt.Errorf("parsing answer file: %w", err)}
	}

	var errs []error
	for _, settings := range doc.Settings {
		if !slices.Contains(unattendPasses, settings.Pass) {
			errs = append(errs, fmt.Errorf("unknown configuration pass %q (expected one of %s)",
				settings.Pass, strings.Join(unattendPasses, ", ")))
			continue
		}
		for _, component := range settings.Components {
			for _, node := range component.Nodes {
				errs = append(errs, validateNode(node, component.Name, settings.Pass)...)
			}
		}
	}
	return errs
}

// validateNode checks one element and everything beneath it.
func validateNode(node xmlNode, component, pass string) []error {
	var errs []error
	name := node.XMLName.Local

	if reason, banned := bannedElements[name]; banned {
		errs = append(errs, fmt.Errorf("<%s> must not be used: %s", name, reason))
	}

	if components, known := unattendElementComponents[name]; known {
		if !slices.Contains(components, component) {
			errs = append(errs, fmt.Errorf(
				"<%s> is under component %q but is defined by %s — Windows Setup ignores it silently (%s)",
				name, component, strings.Join(components, " or "), unattendReferenceURL))
		}
	}

	if rule, known := passRules[component+"/"+name]; known {
		if !slices.Contains(rule.Passes, pass) {
			errs = append(errs, fmt.Errorf(
				"<%s> is in pass %q but is only valid in %s (%s%s)",
				name, pass, strings.Join(rule.Passes, ", "), unattendDocBase, rule.Doc))
		}
	}

	for _, child := range node.Nodes {
		errs = append(errs, validateNode(child, component, pass)...)
	}
	return errs
}

// Minimal structures for walking an answer file. Settings and components are
// named explicitly; everything below is a generic tree, since the rules only
// need element names and their enclosing component.

type unattendDoc struct {
	XMLName  xml.Name          `xml:"unattend"`
	Settings []unattendSetting `xml:"settings"`
}

type unattendSetting struct {
	Pass       string              `xml:"pass,attr"`
	Components []unattendComponent `xml:"component"`
}

type unattendComponent struct {
	Name  string    `xml:"name,attr"`
	Nodes []xmlNode `xml:",any"`
}

type xmlNode struct {
	XMLName xml.Name
	Nodes   []xmlNode `xml:",any"`
}
