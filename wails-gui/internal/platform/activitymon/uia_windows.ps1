# Windows UI Automation tree walker.
# Runs as a persistent process: reads JSON requests from stdin, writes JSON
# responses to stdout. Mirrors the protocol used by ocr_windows.ps1 (long-lived
# subprocess, "READY" sentinel, one JSON request per line, one JSON response
# per line).
#
# Protocol
#   Startup: prints "READY" when initialized.
#   Request:
#     {"mode":"text|tree|tree_by_hwnd","hwnd":<int|null>,
#      "budget":{"maxNodes":<int>,"maxDepth":<int>,"maxBytes":<int>}}
#     - mode "text": flat text from the foreground window's UIA tree
#     - mode "tree": AXNode JSON tree of the foreground window
#     - mode "tree_by_hwnd": AXNode JSON tree of a specific HWND (diag tool)
#     hwnd is optional; when omitted the foreground window is used.
#     budget is optional; defaults match macOS (8000 nodes / 40 depth / 1MB).
#   Response (text):  {"text":"..."}
#   Response (tree):  {"tree":{...AXNode...},"nodeCount":<int>,"truncated":<bool>}
#   Error response:   {"error":"..."}
#   Shutdown: send empty line or "EXIT".
#
# Implementation notes
#   - Uses the .NET System.Windows.Automation managed wrapper (UIA v1
#     bridge). Loaded via Add-Type on startup. Forces an STA-friendly path
#     (the wrapper is itself MTA-safe).
#   - We walk the ControlView TreeWalker rather than raw UIA descendants so
#     accessibility-irrelevant chrome (scrollbars, raw panes) is skipped.
#   - BoundingRectangle is in physical pixels; we divide by the focused
#     window's DPI scale to keep frames comparable to macOS device-
#     independent points.
#   - Property reads are batched via CacheRequest to amortize the cross-
#     process round trip. Without caching a 5000-node Slack tree takes
#     5-10 seconds; with caching it lands in 200-500 ms.

try {
    # Write UTF-8 directly to the stdout pipe, bypassing Console.Out encoding
    # which is unreliable in windowless subprocesses on Windows. Same pattern
    # as ocr_windows.ps1.
    $utf8 = New-Object System.Text.UTF8Encoding $false
    $stdout = New-Object System.IO.StreamWriter([Console]::OpenStandardOutput(), $utf8)
    $stdout.AutoFlush = $true
    $stdin  = New-Object System.IO.StreamReader([Console]::OpenStandardInput(), $utf8)

    Add-Type -AssemblyName UIAutomationClient
    Add-Type -AssemblyName UIAutomationTypes
    Add-Type -AssemblyName WindowsBase

    # Pull common types into shorter aliases for readability.
    $AE       = [System.Windows.Automation.AutomationElement]
    $TreeView = [System.Windows.Automation.TreeWalker]::ControlViewWalker
    $ValueP   = [System.Windows.Automation.ValuePattern]
    $TextP    = [System.Windows.Automation.TextPattern]

    # Properties we want UIA to prefetch for every node. Each entry in this
    # request is one fewer cross-process round trip per node. The CacheRequest
    # is applied via Activate() once at startup and reused for every walk.
    $cacheReq = New-Object System.Windows.Automation.CacheRequest
    $cacheReq.TreeFilter = [System.Windows.Automation.Automation]::ControlViewCondition
    $cacheReq.TreeScope  = [System.Windows.Automation.TreeScope]::Element -bor `
                           [System.Windows.Automation.TreeScope]::Children
    $cacheReq.Add($AE::ControlTypeProperty)
    $cacheReq.Add($AE::LocalizedControlTypeProperty)
    $cacheReq.Add($AE::ClassNameProperty)
    $cacheReq.Add($AE::NameProperty)
    $cacheReq.Add($AE::AutomationIdProperty)
    $cacheReq.Add($AE::HelpTextProperty)
    $cacheReq.Add($AE::BoundingRectangleProperty)
    $cacheReq.Add($AE::IsOffscreenProperty)
    $cacheReq.Add($AE::IsContentElementProperty)
    $cacheReq.Add($AE::IsKeyboardFocusableProperty)
    $cacheReq.Add($AE::HasKeyboardFocusProperty)
    $cacheReq.Add($AE::ControlTypeProperty)
    $cacheReq.Add($ValueP::Pattern)
    $cacheReq.Add($ValueP::ValueProperty)

    # P/Invoke for HWND -> DPI lookup. GetDpiForWindow is Windows 10 1607+.
    # Older systems fall back to GetDeviceCaps(GetDC(hwnd), LOGPIXELSX).
    Add-Type -Namespace W -Name Pinvoke -MemberDefinition @"
        [System.Runtime.InteropServices.DllImport("user32.dll")]
        public static extern System.IntPtr GetForegroundWindow();
        [System.Runtime.InteropServices.DllImport("user32.dll")]
        public static extern uint GetDpiForWindow(System.IntPtr hWnd);
        [System.Runtime.InteropServices.DllImport("user32.dll")]
        public static extern System.IntPtr GetDC(System.IntPtr hWnd);
        [System.Runtime.InteropServices.DllImport("user32.dll")]
        public static extern int ReleaseDC(System.IntPtr hWnd, System.IntPtr hDC);
        [System.Runtime.InteropServices.DllImport("gdi32.dll")]
        public static extern int GetDeviceCaps(System.IntPtr hdc, int nIndex);
        public const int LOGPIXELSX = 88;
"@

    function Get-DpiScale($hwnd) {
        $dpi = 0
        try { $dpi = [W.Pinvoke]::GetDpiForWindow($hwnd) } catch {}
        if ($dpi -le 0) {
            $hdc = [W.Pinvoke]::GetDC([IntPtr]::Zero)
            if ($hdc -ne [IntPtr]::Zero) {
                $dpi = [W.Pinvoke]::GetDeviceCaps($hdc, [W.Pinvoke]::LOGPIXELSX)
                [W.Pinvoke]::ReleaseDC([IntPtr]::Zero, $hdc) | Out-Null
            }
        }
        if ($dpi -le 0) { $dpi = 96 }
        return [double]$dpi / 96.0
    }

    # ControlType.Id -> macOS-style AXRole string. The mapping is best-effort:
    # macOS parsers (Slack/ChatGPT/Notion/etc) key off AXRole values, so picking
    # the closest match keeps the existing parsers reachable without per-app
    # forking. Values that have no clear macOS equivalent fall through to a
    # leading "AX" + LocalizedControlType name (which downstream parsers
    # generally treat as opaque).
    $roleMap = @{
        50000 = 'AXButton'        # Button
        50001 = 'AXCalendar'      # Calendar
        50002 = 'AXCheckBox'      # CheckBox
        50003 = 'AXComboBox'      # ComboBox
        50004 = 'AXTextField'     # Edit
        50005 = 'AXLink'          # Hyperlink
        50006 = 'AXImage'         # Image
        50007 = 'AXRow'           # ListItem  -> map to AXRow so slide-navigator / file-list parsers match macOS
        50008 = 'AXList'          # List
        50009 = 'AXMenu'          # Menu
        50010 = 'AXMenuBar'       # MenuBar
        50011 = 'AXMenuItem'      # MenuItem
        50012 = 'AXProgressIndicator' # ProgressBar
        50013 = 'AXRadioButton'   # RadioButton
        50014 = 'AXScrollBar'     # ScrollBar
        50015 = 'AXSlider'        # Slider
        50016 = 'AXValueIndicator' # Spinner
        50017 = 'AXStatusBar'     # StatusBar
        50018 = 'AXTab'           # Tab
        50019 = 'AXTab'           # TabItem
        50020 = 'AXTextArea'      # Text  -> AXTextArea covers static + editable blocks; AXStaticText handled below for IsContentElement leaves
        50021 = 'AXToolbar'       # ToolBar
        50022 = 'AXTooltip'       # ToolTip
        50023 = 'AXOutline'       # Tree
        50024 = 'AXRow'           # TreeItem
        50025 = 'AXGroup'         # Custom
        50026 = 'AXGroup'         # Group
        50027 = 'AXSplitter'      # Thumb
        50028 = 'AXTable'         # DataGrid
        50029 = 'AXCell'          # DataItem
        50030 = 'AXDocument'      # Document  -> AXTextArea on macOS for long docs, but AXDocument is recognizable and parsers tolerate either
        50031 = 'AXSplitter'      # SplitButton
        50032 = 'AXWindow'        # Window
        50033 = 'AXSplitter'      # Pane
        50034 = 'AXHeader'        # Header
        50035 = 'AXHeader'        # HeaderItem
        50036 = 'AXTable'         # Table
        50037 = 'AXCell'          # TitleBar
        50038 = 'AXSeparator'     # Separator
        50039 = 'AXStaticText'    # SemanticZoom
        50040 = 'AXAppRegion'     # AppBar
    }

    function ResolveRole($el) {
        try {
            $ct = $el.GetCachedPropertyValue($AE::ControlTypeProperty, $true)
        } catch { $ct = $null }
        if ($null -ne $ct) {
            $id = $null
            if ($ct -is [System.Windows.Automation.ControlType]) { $id = $ct.Id }
            else { try { $id = [int]$ct } catch { $id = $null } }
            if ($null -ne $id -and $roleMap.ContainsKey($id)) { return $roleMap[$id] }
        }
        # Fall back to LocalizedControlType — last-ditch readable string.
        try {
            $loc = $el.GetCachedPropertyValue($AE::LocalizedControlTypeProperty, $true)
            if ($loc) { return 'AX' + ($loc -replace '\s+','') }
        } catch {}
        return 'AXUnknown'
    }

    function ReadCachedString($el, $prop) {
        try {
            $v = $el.GetCachedPropertyValue($prop, $true)
            if ($null -eq $v -or $v -eq [System.Windows.Automation.AutomationElement]::NotSupported) { return '' }
            return [string]$v
        } catch { return '' }
    }

    function TryGetValuePattern($el) {
        try {
            $p = $el.GetCachedPattern($ValueP::Pattern)
            if ($p) { return $p.Current.Value }
        } catch {}
        return ''
    }

    # Escape a string for JSON. We can't use ConvertTo-Json on every leaf
    # (PS 5.1 is slow), so we inline a tight escaper.
    function JsonEscape([string]$s) {
        if (-not $s) { return '""' }
        $sb = New-Object System.Text.StringBuilder ($s.Length + 8)
        [void]$sb.Append('"')
        for ($i = 0; $i -lt $s.Length; $i++) {
            $c = $s[$i]
            $code = [int]$c
            switch ($code) {
                8  { [void]$sb.Append('\b'); continue }
                9  { [void]$sb.Append('\t'); continue }
                10 { [void]$sb.Append('\n'); continue }
                12 { [void]$sb.Append('\f'); continue }
                13 { [void]$sb.Append('\r'); continue }
                34 { [void]$sb.Append('\"'); continue }
                92 { [void]$sb.Append('\\'); continue }
                default {
                    if ($code -lt 32) {
                        [void]$sb.AppendFormat('\u{0:x4}', $code)
                    } else {
                        [void]$sb.Append($c)
                    }
                }
            }
        }
        [void]$sb.Append('"')
        return $sb.ToString()
    }

    # Tree walk state. Held in a hashtable so the recursive function can
    # mutate counters without scope gymnastics.
    function New-WalkState($maxNodes, $maxDepth, $maxBytes, $dpiScale) {
        return @{
            Sb         = New-Object System.Text.StringBuilder
            NodeCount  = 0
            MaxNodes   = $maxNodes
            MaxDepth   = $maxDepth
            MaxBytes   = $maxBytes
            DpiScale   = $dpiScale
            Truncated  = $false
            FlatText   = $null   # set to a StringBuilder for text mode
            # Sliding window of recently appended flat-text entries used to
            # suppress the parent/child duplication endemic to Chromium UIA
            # trees: Slack / Notion / Discord render each message as a
            # nested AXLink + container + StaticText where every layer
            # exposes the SAME body text via Name / Value / Help. Without
            # dedup the user sees each message body twice or three times
            # in a row. Backed by a Queue so insertion + Contains are both
            # fast at this size; the explicit size cap is the only thing
            # protecting it from growing without bound.
            Recent     = New-Object System.Collections.Generic.Queue[string]
            RecentMax  = 8
        }
    }

    function AppendFlatText($st, [string]$text) {
        if ($null -eq $st.FlatText) { return }
        if (-not $text) { return }
        $trim = $text.Trim()
        if (-not $trim) { return }
        if ($st.Recent.Contains($trim)) { return }
        if ($st.FlatText.Length -gt 0) { [void]$st.FlatText.Append("`n") }
        [void]$st.FlatText.Append($trim)
        $st.Recent.Enqueue($trim)
        while ($st.Recent.Count -gt $st.RecentMax) {
            [void]$st.Recent.Dequeue()
        }
    }

    function WalkElement($el, [int]$depth, $st) {
        if (-not $el) { return }
        if ($st.NodeCount -ge $st.MaxNodes -or $depth -ge $st.MaxDepth) {
            $st.Truncated = $true
            return
        }
        if ($st.Sb.Length -ge $st.MaxBytes - 512) {
            $st.Truncated = $true
            return
        }
        $st.NodeCount++

        $role = ResolveRole $el
        # AXScrollBar is purely decorative; macOS walker also skips it.
        if ($role -eq 'AXScrollBar' -or $role -eq 'AXTooltip') { return }

        # IsOffscreen elements still get walked because some apps mark
        # virtual-list rows offscreen between renders even though they hold
        # real content. The frame ends up 0 / negative — downstream layout
        # parsers tolerate that.

        $name        = ReadCachedString $el $AE::NameProperty
        $className   = ReadCachedString $el $AE::ClassNameProperty
        $automationId = ReadCachedString $el $AE::AutomationIdProperty
        $help        = ReadCachedString $el $AE::HelpTextProperty
        $value       = TryGetValuePattern $el

        # If we are in text-collection mode, append the leaf-ish content
        # before recursing into children. The role gate matches the macOS
        # axwalk_should_extract list.
        $extractRoles = @{
            'AXStaticText' = $true; 'AXTextField' = $true; 'AXTextArea' = $true
            'AXButton' = $true; 'AXMenuItem' = $true; 'AXCell' = $true
            'AXHeading' = $true; 'AXLink' = $true; 'AXTab' = $true
            'AXRow' = $true; 'AXCheckBox' = $true; 'AXRadioButton' = $true
            'AXDocument' = $true
        }
        if ($extractRoles.ContainsKey($role)) {
            if ($value) { AppendFlatText $st $value }
            elseif ($name) { AppendFlatText $st $name }
            elseif ($help) { AppendFlatText $st $help }
        } elseif ($role -eq 'AXGroup' -and $value) {
            AppendFlatText $st $value
        }

        # Demote Text-class leaves to AXStaticText when no value/pattern
        # surface (matches what macOS calls them, makes parsers' role
        # filters fire). We do this after extracting so the flat-text
        # path sees the same name.
        if ($role -eq 'AXTextArea' -and -not $value) {
            $role = 'AXStaticText'
        }

        [void]$st.Sb.Append('{"role":')
        [void]$st.Sb.Append((JsonEscape $role))
        if ($className) {
            [void]$st.Sb.Append(',"subrole":')
            [void]$st.Sb.Append((JsonEscape $className))
        }
        if ($name) {
            [void]$st.Sb.Append(',"title":')
            [void]$st.Sb.Append((JsonEscape $name))
        }
        if ($help) {
            [void]$st.Sb.Append(',"description":')
            [void]$st.Sb.Append((JsonEscape $help))
        }
        if ($value) {
            [void]$st.Sb.Append(',"value":')
            [void]$st.Sb.Append((JsonEscape $value))
        }
        if ($automationId) {
            [void]$st.Sb.Append(',"identifier":')
            [void]$st.Sb.Append((JsonEscape $automationId))
        }

        try {
            $hasFocus = $el.GetCachedPropertyValue($AE::HasKeyboardFocusProperty, $true)
            if ($hasFocus -eq $true) {
                [void]$st.Sb.Append(',"selected":true')
            }
        } catch {}

        # Frame: BoundingRectangle is a Rect (System.Windows.Rect) in pixels
        # for UIA. Divide by DPI so the value matches macOS's device-
        # independent points.
        try {
            $rect = $el.GetCachedPropertyValue($AE::BoundingRectangleProperty, $true)
            if ($rect -and -not $rect.IsEmpty -and $rect.Width -gt 0 -and $rect.Height -gt 0) {
                $s = $st.DpiScale
                if ($s -le 0) { $s = 1.0 }
                $fx = [math]::Round($rect.X / $s, 1)
                $fy = [math]::Round($rect.Y / $s, 1)
                $fw = [math]::Round($rect.Width / $s, 1)
                $fh = [math]::Round($rect.Height / $s, 1)
                [void]$st.Sb.AppendFormat([System.Globalization.CultureInfo]::InvariantCulture,
                    ',"frame":{{"x":{0},"y":{1},"w":{2},"h":{3}}}', $fx, $fy, $fw, $fh)
            }
        } catch {}

        # Children. We use TreeWalker (ControlView) rather than FindAll to
        # avoid pulling the entire descendant set on every node — we want
        # depth-first with budget short-circuit.
        $child = $null
        try { $child = $TreeView.GetFirstChild($el, $cacheReq) } catch { $child = $null }
        if ($child) {
            $beforeChildren = $st.Sb.Length
            [void]$st.Sb.Append(',"children":[')
            $emitted = 0
            while ($child) {
                if ($st.NodeCount -ge $st.MaxNodes) { $st.Truncated = $true; break }
                $beforeWalk = $st.Sb.Length
                if ($emitted -gt 0) { [void]$st.Sb.Append(',') }
                $commaPos = $st.Sb.Length
                WalkElement $child ($depth + 1) $st
                if ($st.Sb.Length -eq $commaPos) {
                    # Walk produced nothing — undo the comma (matches macOS
                    # tree_walk roll-back logic so we never emit "[,]").
                    if ($emitted -gt 0) { $st.Sb.Length = $beforeWalk }
                } else {
                    $emitted++
                }
                $next = $null
                try { $next = $TreeView.GetNextSibling($child, $cacheReq) } catch { $next = $null }
                $child = $next
            }
            if ($emitted -eq 0) {
                # All children skipped — roll back the ,"children":[ prefix.
                $st.Sb.Length = $beforeChildren
            } else {
                [void]$st.Sb.Append(']')
            }
        }

        [void]$st.Sb.Append('}')
    }

    function GetForegroundElement([IntPtr]$hwnd) {
        if ($hwnd -eq [IntPtr]::Zero) {
            $hwnd = [W.Pinvoke]::GetForegroundWindow()
        }
        if ($hwnd -eq [IntPtr]::Zero) { return $null, [IntPtr]::Zero }
        # Activate the CacheRequest BEFORE FromHandle so the root
        # element itself comes back with its properties cached.
        # Without this, GetCachedPropertyValue on the root throws
        # InvalidOperationException (swallowed by try/catch in
        # ResolveRole / ReadCachedString below), the root node ends
        # up with role="AXUnknown" and no title / frame, and any
        # parser keying on the AXWindow root sees an empty hull.
        # CacheRequest.Activate() returns an IDisposable; disposing
        # pops the request from the thread's cache-request stack.
        $disp = $cacheReq.Activate()
        try {
            $el = $AE::FromHandle($hwnd)
            return $el, $hwnd
        } catch {
            return $null, $hwnd
        } finally {
            if ($disp) { $disp.Dispose() }
        }
    }

    $stdout.WriteLine('READY')
} catch {
    try { [Console]::Error.WriteLine($_.Exception.ToString()) } catch {}
    try { $stdout.WriteLine('ERROR') } catch {}
    exit 1
}

# Main loop.
while ($true) {
    $line = $stdin.ReadLine()
    if ([string]::IsNullOrEmpty($line) -or $line -eq 'EXIT') { break }

    try {
        $req = $line | ConvertFrom-Json

        $maxNodes = 8000
        $maxDepth = 40
        $maxBytes = 1048576
        if ($req.budget) {
            if ($req.budget.maxNodes) { $maxNodes = [int]$req.budget.maxNodes }
            if ($req.budget.maxDepth) { $maxDepth = [int]$req.budget.maxDepth }
            if ($req.budget.maxBytes) { $maxBytes = [int]$req.budget.maxBytes }
        }

        $targetHwnd = [IntPtr]::Zero
        if ($req.hwnd) {
            $targetHwnd = [IntPtr][int64]$req.hwnd
        }

        $el, $hwnd = GetForegroundElement $targetHwnd
        if (-not $el) {
            $stdout.WriteLine('{"error":"no foreground window"}')
            continue
        }

        $dpiScale = Get-DpiScale $hwnd
        $st = New-WalkState $maxNodes $maxDepth $maxBytes $dpiScale

        $mode = $req.mode
        if (-not $mode) { $mode = 'tree' }

        switch ($mode) {
            'text' {
                $st.FlatText = New-Object System.Text.StringBuilder
                # Reuse the cached $el GetForegroundElement returned —
                # re-fetching via FromHandle here without re-activating
                # the CacheRequest would drop the root's cached
                # properties and make WalkElement see role="AXUnknown".
                WalkElement $el 0 $st
                $payload = '{"text":' + (JsonEscape ($st.FlatText.ToString())) + '}'
                $stdout.WriteLine($payload)
            }
            'tree' {
                WalkElement $el 0 $st
                if ($st.Sb.Length -eq 0) {
                    $stdout.WriteLine('{"error":"empty tree"}')
                } else {
                    $stdout.Write('{"tree":')
                    $stdout.Write($st.Sb.ToString())
                    $stdout.Write(',"nodeCount":')
                    $stdout.Write($st.NodeCount)
                    $stdout.Write(',"truncated":')
                    if ($st.Truncated) { $stdout.Write('true') } else { $stdout.Write('false') }
                    $stdout.WriteLine('}')
                }
            }
            'tree_by_hwnd' {
                WalkElement $el 0 $st
                if ($st.Sb.Length -eq 0) {
                    $stdout.WriteLine('{"error":"empty tree"}')
                } else {
                    $stdout.Write('{"tree":')
                    $stdout.Write($st.Sb.ToString())
                    $stdout.Write(',"nodeCount":')
                    $stdout.Write($st.NodeCount)
                    $stdout.Write(',"truncated":')
                    if ($st.Truncated) { $stdout.Write('true') } else { $stdout.Write('false') }
                    $stdout.WriteLine('}')
                }
            }
            default {
                $stdout.WriteLine('{"error":"unknown mode"}')
            }
        }
    } catch {
        $msg = ($_.Exception.Message -replace '\\','\\\\' -replace '"','\"' -replace "`r",'' -replace "`n",' ')
        $stdout.WriteLine('{"error":"' + $msg + '"}')
    }
}
