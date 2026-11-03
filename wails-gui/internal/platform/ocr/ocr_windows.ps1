# Windows OCR engine using Windows.Media.Ocr WinRT API.
# Runs as a persistent process: reads JSON requests from stdin, writes JSON responses to stdout.
# Protocol:
#   Startup: prints "READY" when initialized.
#   Request: {"mode":"text|boxes","path":"C:\\...\\image.jpg","langs":["en-US","zh-Hans"]}
#   Response (text):  {"text":"...","confidence":1.0}
#   Response (boxes): {"observations":[{"text":"...","confidence":1.0,"x":0.1,"y":0.2,"width":0.5,"height":0.05}]}
#   Error response:   {"error":"..."}
#   Shutdown: send empty line or "EXIT".

try {
    # Write UTF-8 directly to the stdout pipe, bypassing Console.Out encoding
    # which is unreliable in windowless subprocesses on Windows.
    $utf8 = New-Object System.Text.UTF8Encoding $false
    $stdout = New-Object System.IO.StreamWriter([Console]::OpenStandardOutput(), $utf8)
    $stdout.AutoFlush = $true

    # Also fix stdin encoding for reading JSON requests with CJK characters.
    $stdin = New-Object System.IO.StreamReader([Console]::OpenStandardInput(), $utf8)

    Add-Type -AssemblyName System.Runtime.WindowsRuntime

    [Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType=WindowsRuntime] > $null
    [Windows.Graphics.Imaging.BitmapDecoder, Windows.Foundation, ContentType=WindowsRuntime] > $null
    [Windows.Globalization.Language, Windows.Foundation, ContentType=WindowsRuntime] > $null

    # Build the generic type name for IAsyncOperation`1 without using a literal backtick.
    $asyncOpName = 'IAsyncOperation' + [char]96 + '1'
    $asTaskGeneric = ([System.WindowsRuntimeSystemExtensions].GetMethods() |
        Where-Object {
            $_.Name -eq 'AsTask' -and
            $_.GetParameters().Count -eq 1 -and
            $_.GetParameters()[0].ParameterType.Name -eq $asyncOpName
        })[0]

    function Await($WinRtTask, $ResultType) {
        $asTask = $asTaskGeneric.MakeGenericMethod($ResultType)
        $netTask = $asTask.Invoke($null, @($WinRtTask))
        $netTask.Wait(-1) > $null
        $netTask.Result
    }

    # Pre-create engines for known languages.
    # Each OcrEngine supports one language; we cache them by language tag.
    $engines = @{}
    $defaultEngine = $null

    function Get-OcrEngine($langTag) {
        if ($engines.ContainsKey($langTag)) { return $engines[$langTag] }

        # Map short tags to Windows language tags.
        $mappings = @{
            'zh-Hans' = 'zh-Hans-CN'
            'zh-Hant' = 'zh-Hant-TW'
        }
        $winTag = $langTag
        if ($mappings.ContainsKey($langTag)) { $winTag = $mappings[$langTag] }

        $lang = [Windows.Globalization.Language]::new($winTag)
        if ([Windows.Media.Ocr.OcrEngine]::IsLanguageSupported($lang)) {
            $eng = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($lang)
            if ($eng) {
                $engines[$langTag] = $eng
                return $eng
            }
        }
        return $null
    }

    # Fallback engine from user profile.
    $defaultEngine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages()
    if (-not $defaultEngine) {
        [Console]::Error.WriteLine('no OCR engine available')
        $stdout.WriteLine('ERROR')
        exit 1
    }

    $stdout.WriteLine('READY')
} catch {
    [Console]::Error.WriteLine($_.Exception.ToString())
    $stdout.WriteLine('ERROR')
    exit 1
}

# Main processing loop.
while ($true) {
    $line = $stdin.ReadLine()
    if ([string]::IsNullOrEmpty($line) -or $line -eq 'EXIT') { break }

    try {
        $req = $line | ConvertFrom-Json

        # Pick the best OCR engine for the requested languages.
        # CJK engines (zh, ja, ko) also recognize Latin text, so prefer them
        # when multiple languages are requested.
        $engine = $null
        if ($req.langs -and $req.langs.Count -gt 0) {
            foreach ($lt in $req.langs) {
                if ($lt.StartsWith('zh-') -or $lt.StartsWith('ja') -or $lt.StartsWith('ko')) {
                    $engine = Get-OcrEngine $lt
                    if ($engine) { break }
                }
            }
            if (-not $engine) {
                foreach ($lt in $req.langs) {
                    $engine = Get-OcrEngine $lt
                    if ($engine) { break }
                }
            }
        }
        if (-not $engine) { $engine = $defaultEngine }

        # Read JPEG from file path.
        $fileStream = [System.IO.File]::OpenRead($req.path)
        $rasStream = [System.IO.WindowsRuntimeStreamExtensions]::AsRandomAccessStream($fileStream)

        $decoder = Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($rasStream)) ([Windows.Graphics.Imaging.BitmapDecoder])
        $bitmap  = Await ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
        $ocrRes  = Await ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])

        $ocrText = $ocrRes.Text
        if (-not $ocrText) { $ocrText = '' }

        if ($req.mode -eq 'boxes') {
            $imgW = $bitmap.PixelWidth
            $imgH = $bitmap.PixelHeight

            # Build JSON manually to avoid PowerShell 5.1 array serialization quirks.
            $parts = [System.Collections.Generic.List[string]]::new()

            if ($imgW -gt 0 -and $imgH -gt 0) {
                foreach ($ocrLine in $ocrRes.Lines) {
                    $lt = $ocrLine.Text
                    $words = $ocrLine.Words
                    if ($words.Count -eq 0) { continue }

                    $minX = [double]::MaxValue
                    $minY = [double]::MaxValue
                    $maxX = 0.0
                    $maxY = 0.0
                    foreach ($w in $words) {
                        $r = $w.BoundingRect
                        if ($r.X -lt $minX) { $minX = $r.X }
                        if ($r.Y -lt $minY) { $minY = $r.Y }
                        $rx = $r.X + $r.Width
                        $ry = $r.Y + $r.Height
                        if ($rx -gt $maxX) { $maxX = $rx }
                        if ($ry -gt $maxY) { $maxY = $ry }
                    }

                    # Use ConvertTo-Json for safe text escaping (handles all Unicode and control chars).
                    $escaped = ($lt | ConvertTo-Json -Compress)
                    $nx  = [Math]::Round($minX / $imgW, 6)
                    $ny  = [Math]::Round($minY / $imgH, 6)
                    $nw  = [Math]::Round(($maxX - $minX) / $imgW, 6)
                    $nh  = [Math]::Round(($maxY - $minY) / $imgH, 6)
                    $parts.Add('{"text":' + $escaped + ',"confidence":1,"x":' + $nx + ',"y":' + $ny + ',"width":' + $nw + ',"height":' + $nh + '}')
                }
            }

            $json = '{"observations":[' + ($parts -join ',') + ']}'
        } else {
            $conf = 1.0
            if ($ocrText.Length -eq 0) { $conf = 0.0 }
            $escaped = ($ocrText | ConvertTo-Json -Compress)
            $json = '{"text":' + $escaped + ',"confidence":' + $conf + '}'
        }

        $bitmap.Dispose()
        $fileStream.Close()
        $fileStream.Dispose()

        $stdout.WriteLine($json)
    } catch {
        $errMsg = $_.Exception.Message -replace '\\', '\\\\' -replace '"', '\"' -replace "`r", '' -replace "`n", ' '
        $stdout.WriteLine('{"error":"' + $errMsg + '"}')
    }
}
