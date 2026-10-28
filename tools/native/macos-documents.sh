#!/bin/bash
# Окно поступлений на macOS: окно приложения показано (есть окно у процесса), снимок экрана — артефакт. Нажатия клавиш не
# вводятся: у раннера нет прав Accessibility; это ограничение записано в docs/implementation/native-platforms.md.
set -euo pipefail
cfg="$PWD/testdata/configs/small-stock"
bin="$PWD/oddyness"
shots="${ODDYNESS_SHOTS:?}"
mkdir -p "$shots"
db="$(mktemp -d)/mac-documents.sqlite"
"$bin" db create "$cfg" "$db"
"$bin" catalog add "$cfg" "$db" goods 'Код=K-1' 'Наименование=Кабель' 'ЕдиницаИзмерения=м' 'Используется=true' 'МинимальныйОстаток=0'
"$bin" documents "$cfg" "$db" receipt &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT
cat > "$RUNNER_TEMP/windows.swift" <<'SWIFT'
import Cocoa
let pid = Int32(CommandLine.arguments[1])!
let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly], kCGNullWindowID) as? [[String: Any]] ?? []
var found = 0
for w in list where (w[kCGWindowOwnerPID as String] as? Int32) == pid {
    let b = w[kCGWindowBounds as String] as? [String: Any] ?? [:]
    let width = (b["Width"] as? Double) ?? 0, height = (b["Height"] as? Double) ?? 0
    print("window of pid \(pid): title=\(w[kCGWindowName as String] ?? "-") size=\(width)x\(height)")
    if width > 100 && height > 100 { found += 1 }
}
exit(found > 0 ? 0 : 1)
SWIFT
ok=0
for _ in $(seq 1 60); do
  if ! kill -0 "$pid" 2>/dev/null; then echo "the application exited before showing a window"; exit 1; fi
  if swift "$RUNNER_TEMP/windows.swift" "$pid"; then ok=1; break; fi
  sleep 1
done
sleep 1
screencapture -x "$shots/documents-cocoa.png" || echo "screencapture failed (no screen recording permission?)"
test "$ok" = 1 || { echo "no window of the application appeared"; exit 1; }
kill -0 "$pid" 2>/dev/null || { echo "the application died after showing the window"; exit 1; }
echo "macOS window scenario passed"
