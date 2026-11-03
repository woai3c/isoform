//go:build windows

package notify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wails-gui/internal/platform/cmdutil"
)

const appUserModelID = "Yansu.Desktop"

// xmlEscape escapes special XML characters in a string.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// Send displays a desktop notification using the Windows Toast Notification
// (WinRT) API via PowerShell.
//
// A custom AppUserModelID is registered under HKCU so that the notification
// displays "Isoform" as the app name instead of "Windows PowerShell".
//
// If opts.IconPath points to a valid .ico file its directory is checked for a
// sibling appicon.png which is used as the toast logo override; the .ico path
// is stored as the AUMID registry icon.
func Send(title, body string, opts *Options) error {
	icoPath := ""
	pngPath := ""
	if opts != nil && opts.IconPath != "" {
		if strings.HasSuffix(strings.ToLower(opts.IconPath), ".ico") {
			if _, err := os.Stat(opts.IconPath); err == nil {
				icoPath = opts.IconPath
			}
		}
		// Look for sibling .png for toast appLogoOverride.
		dir := filepath.Dir(opts.IconPath)
		candidate := filepath.Join(dir, "appicon.png")
		if _, err := os.Stat(candidate); err == nil {
			pngPath = candidate
		}
	}

	// Build the AUMID registry registration snippet.
	regSnippet := fmt.Sprintf(
		`$aumid='%s';`+
			`$rp="HKCU:\Software\Classes\AppUserModelId\$aumid";`+
			`if(-not(Test-Path $rp)){New-Item -Path $rp -Force|Out-Null};`+
			`Set-ItemProperty -Path $rp -Name DisplayName -Value 'Isoform'`,
		appUserModelID,
	)
	if icoPath != "" {
		regSnippet += fmt.Sprintf(
			`;Set-ItemProperty -Path $rp -Name IconUri -Value '%s'`,
			strings.ReplaceAll(icoPath, "'", "''"),
		)
	}
	regSnippet += ";"

	// Build toast XML.
	imageElement := ""
	if pngPath != "" {
		imageElement = fmt.Sprintf(
			`<image placement="appLogoOverride" src="file:///%s"/>`,
			xmlEscape(filepath.ToSlash(pngPath)),
		)
	}
	toastXML := fmt.Sprintf(
		`<toast><visual><binding template="ToastGeneric">%s<text>%s</text><text>%s</text></binding></visual></toast>`,
		imageElement,
		xmlEscape(title),
		xmlEscape(body),
	)
	// Escape for PowerShell single-quoted string.
	psXML := strings.ReplaceAll(toastXML, "'", "''")

	psScript := regSnippet +
		`[Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime]|Out-Null;` +
		`[Windows.Data.Xml.Dom.XmlDocument,Windows.Data.Xml.Dom,ContentType=WindowsRuntime]|Out-Null;` +
		`$x=New-Object Windows.Data.Xml.Dom.XmlDocument;` +
		fmt.Sprintf(`$x.LoadXml('%s');`, psXML) +
		`$t=New-Object Windows.UI.Notifications.ToastNotification $x;` +
		fmt.Sprintf(`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('%s').Show($t)`, appUserModelID)

	return cmdutil.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript).Run()
}
