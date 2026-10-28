# Сценарий окна поступлений на Windows: настоящее окно Win32, настоящие нажатия клавиш (SendKeys).
# Те же шаги и порядок Tab, что у TestNativeDocuments/receipt (Linux): шапка, строка с пустым товаром (ошибка поля),
# строка, «Сохранить», «Провести», «Закрыть»; затем независимое чтение базы консольным oddyness.
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$cfg = (Resolve-Path 'testdata/configs/small-stock').Path
$exe = (Resolve-Path './oddyness.exe').Path
$gui = (Resolve-Path './oddyness-gui.exe').Path
$shots = $env:ODDYNESS_SHOTS
New-Item -ItemType Directory -Force -Path $shots | Out-Null
$db = Join-Path $env:RUNNER_TEMP 'win-documents.sqlite'
if (Test-Path $db) { Remove-Item $db }

function Shot($name) {
    $b = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
    $bmp = New-Object System.Drawing.Bitmap $b.Width, $b.Height
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.CopyFromScreen($b.Location, [System.Drawing.Point]::Empty, $b.Size)
    $bmp.Save((Join-Path $shots "$name.png"))
    $g.Dispose(); $bmp.Dispose()
}
function Run-Cli {
    $out = & $exe @args 2>&1
    if ($LASTEXITCODE -ne 0) { throw "oddyness $($args -join ' ') failed ($LASTEXITCODE): $out" }
    return ($out -join "`n")
}
function Keys($text) { [System.Windows.Forms.SendKeys]::SendWait($text); Start-Sleep -Milliseconds 400 }
function Tabs($n) { Keys ('{TAB}' * $n) }

Run-Cli db create $cfg $db | Out-Null
Run-Cli catalog add $cfg $db goods 'Код=K-1' 'Наименование=Кабель' 'ЕдиницаИзмерения=м' 'Используется=true' 'МинимальныйОстаток=0' | Out-Null

$p = Start-Process -FilePath $gui -ArgumentList @('documents', $cfg, $db, 'receipt') -PassThru
try {
    $title = 'oddyness documents receipt'
    $deadline = (Get-Date).AddSeconds(60)
    while ($true) {
        $p.Refresh()
        if ($p.HasExited) { throw "the application exited before showing a window (exit code $($p.ExitCode))" }
        if ($p.MainWindowTitle -eq $title) { break }
        if ((Get-Date) -gt $deadline) { Shot 'no-window'; throw "no window titled '$title' (title: '$($p.MainWindowTitle)')" }
        Start-Sleep -Milliseconds 300
    }
    Start-Sleep -Milliseconds 800
    $shell = New-Object -ComObject WScript.Shell
    [void]$shell.AppActivate($p.Id)
    Start-Sleep -Milliseconds 500
    Shot 'before'

    Keys 'R-1'; Tabs 1; Keys '2026-10-28'; Tabs 1; Keys 'batch'
    Tabs 2; Keys '10.500'; Tabs 1; Keys '{ENTER}'      # строка без товара: ошибка поля, окно открыто
    Shot 'line-error'
    if ($p.HasExited) { throw 'the window closed after recording a line without a product' }
    Keys '1'; Tabs 2; Keys '{ENTER}'                  # товар 1, «Записать строку»
    Shot 'line-added'
    if ($p.HasExited) { throw 'the window closed after recording a line' }
    Tabs 5; Keys '{ENTER}'                            # «Сохранить»
    Shot 'saved'
    if ($p.HasExited) { throw 'the window closed after Save' }
    Tabs 9; Keys '{ENTER}'                            # «Провести»
    Shot 'posted'
    if ($p.HasExited) { throw 'the window closed after Post' }
    Tabs 12; Keys '{ENTER}'                           # «Закрыть»
    if (-not $p.WaitForExit(20000)) { Shot 'not-exited'; throw 'the application did not exit after Close' }
    if ($p.ExitCode -ne 0) { throw "the application exited with code $($p.ExitCode)" }
} finally {
    if (-not $p.HasExited) { $p.Kill() }
}

$list = Run-Cli document list $cfg $db receipt
$bal = Run-Cli register balance $cfg $db 'Запасы'
Write-Host "documents:`n$list`nbalance: $bal"
if ($bal.Trim() -ne 'Товар=1 Количество=10.500') { throw "unexpected balance: '$bal'" }
if ($list -notmatch 'R-1') { throw "the document R-1 is not in the base: $list" }
Write-Host 'Windows scenario passed'
