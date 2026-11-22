//go:build windows

package main

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	appTitle         = "Flux Studio – lokal"
	wmDestroy        = 0x0002
	wmCommand        = 0x0111
	wmSetFont        = 0x0030
	wmAppUpdate      = 0x8001
	wmAppLog         = 0x8002
	wsOverlapped     = 0x00CF0000
	wsVisible        = 0x10000000
	wsChild          = 0x40000000
	wsTabStop        = 0x00010000
	wsBorder         = 0x00800000
	esMultiline      = 0x0004
	esAutoVScroll    = 0x0040
	esWantReturn     = 0x1000
	esReadOnly       = 0x0800
	wsVScroll        = 0x00200000
	bsPushButton     = 0x00000000
	cbDropDownList   = 0x0003
	cbAddString      = 0x0143
	cbSetCurSel      = 0x014E
	cbGetCurSel      = 0x0147
	emSetSel         = 0x00B1
	emReplaceSel     = 0x00C2
	ofNFileMustExist = 0x00001000
	ofNPathMustExist = 0x00000800
	idInstall        = 1001
	idCheckGPU       = 1002
	idMotif          = 1003
	idEnvironment    = 1004
	idStyle          = 1005
	idLight          = 1006
	idExtra          = 1007
	idPickImage      = 1008
	idClearImage     = 1009
	idResolution     = 1010
	idGenerate       = 1011
	idOpenFolder     = 1012
	idOpenLast       = 1013
	idLog            = 1014
	idStatus         = 1015
)

var (
	user32             = syscall.NewLazyDLL("user32.dll")
	gdi32              = syscall.NewLazyDLL("gdi32.dll")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	comdlg32           = syscall.NewLazyDLL("comdlg32.dll")
	shell32            = syscall.NewLazyDLL("shell32.dll")
	procRegCls         = user32.NewProc("RegisterClassExW")
	procCreate         = user32.NewProc("CreateWindowExW")
	procDefProc        = user32.NewProc("DefWindowProcW")
	procShow           = user32.NewProc("ShowWindow")
	procUpdate         = user32.NewProc("UpdateWindow")
	procGetMsg         = user32.NewProc("GetMessageW")
	procTrans          = user32.NewProc("TranslateMessage")
	procDispatch       = user32.NewProc("DispatchMessageW")
	procPost           = user32.NewProc("PostMessageW")
	procSend           = user32.NewProc("SendMessageW")
	procSetText        = user32.NewProc("SetWindowTextW")
	procGetText        = user32.NewProc("GetWindowTextW")
	procGetTextLen     = user32.NewProc("GetWindowTextLengthW")
	procEnable         = user32.NewProc("EnableWindow")
	procDestroy        = user32.NewProc("DestroyWindow")
	procPostQuit       = user32.NewProc("PostQuitMessage")
	procLoadCursor     = user32.NewProc("LoadCursorW")
	procLoadIcon       = user32.NewProc("LoadIconW")
	procGetStockObject = gdi32.NewProc("GetStockObject")
	procSetBkColor     = gdi32.NewProc("SetBkColor")
	procOpenFile       = comdlg32.NewProc("GetOpenFileNameW")
	procShellExecute   = shell32.NewProc("ShellExecuteW")
	procDiskSpace      = kernel32.NewProc("GetDiskFreeSpaceExW")
	procRtlVersion     = kernel32.NewProc("RtlGetVersion")

	mainHWND        uintptr
	controls        = map[int]uintptr{}
	appRoot         string
	modelDir        string
	engineDir       string
	cliPath         string
	lastOutput      string
	selectedImage   string
	installed       bool
	busy            bool
	stateMu         sync.Mutex
	uiStatus        string
	uiLog           string
	logFile         string
	logMu           sync.Mutex
	wndProcCallback uintptr
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	SmallIcon  uintptr
}

type point struct{ X, Y int32 }
type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type openFileName struct {
	StructSize    uint32
	HwndOwner     uintptr
	HInstance     uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	Hook          uintptr
	TemplateName  *uint16
	ReservedPtr   uintptr
	Reserved      uint32
	FlagsEx       uint32
}

type osVersionInfo struct {
	Size     uint32
	Major    uint32
	Minor    uint32
	Build    uint32
	Platform uint32
	CSD      [128]uint16
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func main() {
	runtime.LockOSThread() // Win32 window and message loop must stay on one OS thread.
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		local, _ = os.UserHomeDir()
		local = filepath.Join(local, "AppData", "Local")
	}
	appRoot = filepath.Join(local, "FluxStudio")
	engineDir = filepath.Join(appRoot, "engine")
	modelDir = filepath.Join(appRoot, "models")
	logFile = filepath.Join(appRoot, "logs", "FluxStudio.log")
	_ = os.MkdirAll(modelDir, 0755)
	_ = os.MkdirAll(filepath.Dir(logFile), 0755)
	logToFile("\r\n--- Flux Studio startet " + time.Now().Format(time.RFC3339) + " ---\r\n")
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("Unerwarteter Programmfehler: %v\r\nDetails: %s", r, logFile)
			logToFile(msg + "\r\n")
			showStartupError("Flux Studio – Programmfehler", msg)
		}
	}()
	cliPath = locateCLI(engineDir)
	installed = isModelInstalled()
	if !initUI() {
		return
	}
}

func logToFile(s string) {
	if logFile == "" {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(logFile), 0755)
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(s)
	_ = f.Close()
}

func showStartupError(title, body string) {
	t, _ := syscall.UTF16PtrFromString(title)
	b, _ := syscall.UTF16PtrFromString(body)
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), 0x10)
}

func win32LastError() uintptr {
	r, _, _ := kernel32.NewProc("GetLastError").Call()
	return r
}

func initUI() bool {
	className, _ := syscall.UTF16PtrFromString("FluxStudioMainWindow")
	windowTitle, _ := syscall.UTF16PtrFromString(appTitle)
	cursor, _, _ := procLoadCursor.Call(0, 32512)
	inst, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	// RT_GROUP_ICON resource ID 1 is embedded in the executable.
	appIcon, _, iconErr := procLoadIcon.Call(inst, 1)
	if appIcon == 0 {
		logToFile(fmt.Sprintf("WARN: EXE-Iconressource 1 konnte nicht geladen werden (Win32=%d).\r\n", iconErr))
	}
	wndProcCallback = syscall.NewCallback(wndProc)
	wc := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: wndProcCallback, Instance: inst, Icon: appIcon, SmallIcon: appIcon, Cursor: cursor, Background: 6, ClassName: className}
	if r, _, _ := procRegCls.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		code := win32LastError()
		msg := fmt.Sprintf("Das Hauptfenster konnte nicht registriert werden (Windows-Fehler %d).\r\nProtokoll: %s", code, logFile)
		logToFile(msg + "\r\n")
		showStartupError("Flux Studio kann nicht starten", msg)
		return false
	}
	style := uintptr(wsOverlapped)
	hwnd, _, _ := procCreate.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowTitle)), style, 80, 60, 1000, 790, 0, 0, inst, 0)
	if hwnd == 0 {
		code := win32LastError()
		msg := fmt.Sprintf("Das Hauptfenster konnte nicht erstellt werden (Windows-Fehler %d).\r\nProtokoll: %s", code, logFile)
		logToFile(msg + "\r\n")
		showStartupError("Flux Studio kann nicht starten", msg)
		return false
	}
	mainHWND = hwnd
	createControls()
	procShow.Call(hwnd, 1)
	procUpdate.Call(hwnd)
	logToFile("Hauptfenster erfolgreich erstellt.\r\n")
	var m msg
	for {
		r, _, _ := procGetMsg.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == -1 {
			code := win32LastError()
			logToFile(fmt.Sprintf("GetMessageW fehlgeschlagen (Windows-Fehler %d).\r\n", code))
			return false
		}
		if int32(r) == 0 {
			break
		}
		procTrans.Call(uintptr(unsafe.Pointer(&m)))
		procDispatch.Call(uintptr(unsafe.Pointer(&m)))
	}
	return true
}

func wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCommand:
		id := int(wParam & 0xffff)
		logToFile(fmt.Sprintf("WM_COMMAND id=%d received.\r\n", id))
		switch id {
		case idInstall:
			startInstall()
		case idCheckGPU:
			checkGPU()
		case idPickImage:
			pickImage()
		case idClearImage:
			selectedImage = ""
			setText(controls[idPickImage], "Bild auswählen …")
			setStatus("Kein Eingabebild ausgewählt. Der nächste Lauf erzeugt ein neues Bild.")
		case idGenerate:
			startGenerate()
		case idOpenFolder:
			openFolder()
		case idOpenLast:
			openLastImage()
		}
		return 0
	case wmAppUpdate:
		stateMu.Lock()
		s := uiStatus
		stateMu.Unlock()
		setText(controls[idStatus], s)
		return 0
	case wmAppLog:
		stateMu.Lock()
		s := uiLog
		stateMu.Unlock()
		setText(controls[idLog], s)
		return 0
	case wmDestroy:
		procPostQuit.Call(0)
		return 0
	}
	r, _, _ := procDefProc.Call(hwnd, uintptr(message), wParam, lParam)
	return r
}

func createControls() {
	font, _, _ := procGetStockObject.Call(17) // DEFAULT_GUI_FONT
	add := func(id int, cls, title string, x, y, w, h int, style uintptr) uintptr {
		c16, _ := syscall.UTF16PtrFromString(cls)
		t16, _ := syscall.UTF16PtrFromString(title)
		hwnd, _, _ := procCreate.Call(0, uintptr(unsafe.Pointer(c16)), uintptr(unsafe.Pointer(t16)), wsChild|wsVisible|style, uintptr(x), uintptr(y), uintptr(w), uintptr(h), mainHWND, uintptr(id), 0, 0)
		if hwnd != 0 {
			controls[id] = hwnd
			procSend.Call(hwnd, wmSetFont, font, 1)
		}
		return hwnd
	}
	label := func(text string, x, y, w int) { add(0, "STATIC", text, x, y, w, 22, 0) }
	add(0, "STATIC", "FLUX.2 Klein 4B – lokale Bildgenerierung auf deiner Grafikkarte", 24, 16, 650, 28, 0)
	add(idInstall, "BUTTON", "Einrichtung starten (ca. 5 GB)", 700, 12, 270, 32, wsTabStop|bsPushButton)
	add(idStatus, "STATIC", "Status: Noch nicht eingerichtet. Klicke auf „Einrichtung starten“.", 24, 52, 940, 38, 0)
	label("Motiv / Objekt", 24, 100, 130)
	add(idMotif, "EDIT", "", 160, 96, 800, 28, wsBorder|wsTabStop)
	label("Umgebung", 24, 140, 130)
	add(idEnvironment, "EDIT", "", 160, 136, 800, 28, wsBorder|wsTabStop)
	label("Stil", 24, 180, 130)
	add(idStyle, "EDIT", "Fotorealistisch", 160, 176, 800, 28, wsBorder|wsTabStop)
	label("Licht / Kamera", 24, 220, 130)
	add(idLight, "EDIT", "Natürliches Licht, detailreiche Aufnahme", 160, 216, 800, 28, wsBorder|wsTabStop)
	label("Weitere Hinweise", 24, 260, 130)
	add(idExtra, "EDIT", "", 160, 256, 800, 72, wsBorder|wsTabStop|esMultiline|esAutoVScroll|esWantReturn|wsVScroll)
	add(idPickImage, "BUTTON", "Bild auswählen …", 24, 344, 180, 32, wsTabStop|bsPushButton)
	add(idClearImage, "BUTTON", "Auswahl löschen", 214, 344, 145, 32, wsTabStop|bsPushButton)
	add(0, "STATIC", "Optional: Bild auswählen, um es mit FLUX.2 Klein zu bearbeiten.", 375, 350, 580, 22, 0)
	label("Ausgabegröße", 24, 398, 130)
	combo := add(idResolution, "COMBOBOX", "", 160, 394, 145, 180, wsTabStop|cbDropDownList)
	for _, s := range []string{"512 x 512", "768 x 768", "1024 x 1024", "1024 x 768", "768 x 1024"} {
		p, _ := syscall.UTF16PtrFromString(s)
		procSend.Call(combo, cbAddString, 0, uintptr(unsafe.Pointer(p)))
	}
	procSend.Call(combo, cbSetCurSel, 0, 0)
	add(idGenerate, "BUTTON", "Bild generieren / bearbeiten", 330, 390, 250, 40, wsTabStop|bsPushButton)
	add(idCheckGPU, "BUTTON", "Vulkan-GPU prüfen", 594, 390, 180, 40, wsTabStop|bsPushButton)
	add(idOpenFolder, "BUTTON", "Ausgabeordner öffnen", 788, 390, 182, 40, wsTabStop|bsPushButton)
	add(idOpenLast, "BUTTON", "Letztes Bild öffnen", 24, 444, 180, 32, wsTabStop|bsPushButton)
	add(0, "STATIC", "Protokoll", 24, 488, 140, 22, 0)
	add(idLog, "EDIT", "", 24, 512, 946, 190, wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly)
	if installed {
		setStatus("Bereit. FLUX.2 Klein 4B und Vulkan-Engine sind installiert.")
		appendLog("FLUX.2 Klein 4B ist installiert. Wähle Motiv und Umgebung und starte eine Generierung.\r\n")
	} else {
		setStatus("Noch nicht eingerichtet. Die erste Einrichtung lädt Engine und Modell (zusammen ungefähr 5 GB).")
		appendLog("Windows-Anwendung bereit. Noch keine Modellgewichte installiert.\r\n")
	}
	setControlsEnabled(true)
}

func addText(hwnd uintptr, text string) {
	p, _ := syscall.UTF16PtrFromString(text)
	procSetText.Call(hwnd, uintptr(unsafe.Pointer(p)))
}
func setText(hwnd uintptr, text string) {
	if hwnd != 0 {
		addText(hwnd, text)
	}
}
func textOf(id int) string {
	h := controls[id]
	if h == 0 {
		return ""
	}
	n, _, _ := procGetTextLen.Call(h)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, int(n)+1)
	procGetText.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}
func setStatus(s string) {
	stateMu.Lock()
	uiStatus = s
	stateMu.Unlock()
	if mainHWND != 0 {
		procPost.Call(mainHWND, wmAppUpdate, 0, 0)
	}
}
func appendLog(s string) {
	stateMu.Lock()
	uiLog += s
	if len(uiLog) > 18000 {
		uiLog = uiLog[len(uiLog)-18000:]
	}
	stateMu.Unlock()
	logToFile(s)
	if mainHWND != 0 {
		procPost.Call(mainHWND, wmAppLog, 0, 0)
	}
}
func setControlsEnabled(on bool) {
	// Keep the form usable before model installation. The Generate button itself
	// explains when the model has not yet been installed; greying it out looked
	// like a dead/non-responsive application.
	for _, id := range []int{idInstall, idMotif, idEnvironment, idStyle, idLight, idExtra, idPickImage, idClearImage, idResolution, idGenerate, idCheckGPU, idOpenFolder, idOpenLast} {
		if h := controls[id]; h != 0 {
			procEnable.Call(h, boolToUintptr(on))
		}
	}
}
func boolToUintptr(v bool) uintptr {
	if v {
		return 1
	}
	return 0
}
func showMessage(title, body string) {
	t, _ := syscall.UTF16PtrFromString(title)
	b, _ := syscall.UTF16PtrFromString(body)
	user32.NewProc("MessageBoxW").Call(mainHWND, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), 0x40)
}

func startInstall() {
	stateMu.Lock()
	if busy {
		stateMu.Unlock()
		return
	}
	busy = true
	stateMu.Unlock()
	if installed {
		showMessage("Flux Studio", "Engine und Modell sind bereits installiert.")
		stateMu.Lock()
		busy = false
		stateMu.Unlock()
		return
	}
	setControlsEnabled(false)
	go func() {
		defer func() { stateMu.Lock(); busy = false; stateMu.Unlock(); setControlsEnabled(true) }()
		free := freeDiskBytes(appRoot)
		if free > 0 && free < 6500*1024*1024 {
			setStatus("Zu wenig freier Speicherplatz. Benötigt werden mindestens etwa 6,5 GB.")
			appendLog("Installation abgebrochen: weniger als 6,5 GB freier Speicherplatz.\r\n")
			return
		}
		_ = os.MkdirAll(engineDir, 0755)
		_ = os.MkdirAll(modelDir, 0755)
		setStatus("1/4 – Lade Vulkan-Engine für Windows herunter …")
		appendLog("Suche aktuelle Windows-x64-Vulkan-Version von stable-diffusion.cpp …\r\n")
		if err := installEngine(); err != nil {
			setStatus("Engine-Download fehlgeschlagen. Details stehen im Protokoll.")
			appendLog("FEHLER: " + err.Error() + "\r\n")
			return
		}
		setStatus("2/4 – Lade FLUX.2 Klein 4B herunter …")
		files := []struct {
			name, url string
			min       int64
		}{
			{"flux-2-klein-4b-q4_0.gguf", "https://huggingface.co/manishdashsharma/flux2-klein-4b-gguf/resolve/main/flux-2-klein-4b-q4_0.gguf?download=true", 1_800_000_000},
			{"Qwen3-4B-Q4_K_S.gguf", "https://huggingface.co/manishdashsharma/flux2-klein-4b-gguf/resolve/main/Qwen3-4B-Q4_K_S.gguf?download=true", 1_800_000_000},
			{"flux2-vae.safetensors", "https://huggingface.co/manishdashsharma/flux2-klein-4b-gguf/resolve/main/flux2-vae.safetensors?download=true", 250_000_000},
		}
		for i, f := range files {
			setStatus(fmt.Sprintf("%d/4 – Lade Modellkomponente %d/3: %s …", i+2, i+1, f.name))
			appendLog("Download: " + f.name + "\r\n")
			path := filepath.Join(modelDir, f.name)
			if st, e := os.Stat(path); e == nil && st.Size() >= f.min {
				appendLog("Bereits vorhanden, übersprungen.\r\n")
				continue
			}
			if err := downloadFile(f.url, path, func(done, total int64) {
				if total > 0 {
					setStatus(fmt.Sprintf("Lade %s – %.1f / %.1f GB", f.name, float64(done)/1e9, float64(total)/1e9))
				} else {
					setStatus(fmt.Sprintf("Lade %s – %.1f GB", f.name, float64(done)/1e9))
				}
			}); err != nil {
				setStatus("Modell-Download fehlgeschlagen.")
				appendLog("FEHLER beim Download von " + f.name + ": " + err.Error() + "\r\n")
				return
			}
			st, e := os.Stat(path)
			if e != nil || st.Size() < f.min {
				setStatus("Download unvollständig.")
				appendLog("FEHLER: Datei ist kleiner als erwartet: " + f.name + "\r\n")
				return
			}
		}
		setStatus("3/4 – Prüfe installierte Komponenten …")
		cliPath = locateCLI(engineDir)
		if cliPath == "" {
			setStatus("sd-cli.exe wurde in der Engine nicht gefunden.")
			appendLog("FEHLER: sd-cli.exe fehlt im Engine-Archiv.\r\n")
			return
		}
		installed = isModelInstalled()
		if !installed {
			setStatus("Einrichtung nicht vollständig.")
			appendLog("Mindestens eine Modellkomponente fehlt oder ist unvollständig.\r\n")
			return
		}
		setStatus("4/4 – Einrichtung abgeschlossen. Du kannst jetzt Bilder generieren.")
		appendLog("Einrichtung abgeschlossen. Engine: " + cliPath + "\r\n")
		appendLog("Das Modell läuft lokal über Vulkan. Für die Bildgenerierung wird keine Cloud verwendet.\r\n")
	}()
}

func installEngine() error {
	client := http.Client{Timeout: 90 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/leejet/stable-diffusion.cpp/releases/latest", nil)
	req.Header.Set("User-Agent", "FluxStudio/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GitHub API: HTTP %d: %s", resp.StatusCode, string(b))
	}
	var rel githubRelease
	if err = json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return err
	}
	assetURL, assetName := "", ""
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if strings.Contains(n, "win-vulkan-x64.zip") {
			assetURL = a.URL
			assetName = a.Name
			break
		}
	}
	if assetURL == "" {
		return errors.New("kein Windows-x64-Vulkan-Archiv in der neuesten Engine-Veröffentlichung gefunden")
	}
	appendLog("Engine-Version: " + rel.TagName + " / " + assetName + "\r\n")
	zipPath := filepath.Join(appRoot, "engine-download.zip")
	if err := downloadFile(assetURL, zipPath, func(done, total int64) {
		if total > 0 {
			setStatus(fmt.Sprintf("Lade Engine – %.0f%%", float64(done)*100/float64(total)))
		}
	}); err != nil {
		return err
	}
	defer os.Remove(zipPath)
	return extractZip(zipPath, engineDir)
}

func downloadFile(url, dest string, progress func(int64, int64)) error {
	client := http.Client{Timeout: 4 * time.Hour}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "FluxStudio/1.0 (local image tool)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	_ = os.MkdirAll(filepath.Dir(dest), 0755)
	tmp := dest + ".part"
	_ = os.Remove(tmp)
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	buf := make([]byte, 1024*1024)
	var done int64
	last := time.Now()
	for {
		n, e := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(tmp)
				return werr
			}
			done += int64(n)
			if progress != nil && time.Since(last) > 700*time.Millisecond {
				progress(done, resp.ContentLength)
				last = time.Now()
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			f.Close()
			os.Remove(tmp)
			return e
		}
	}
	if err = f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err = f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if resp.ContentLength > 0 && done != resp.ContentLength {
		os.Remove(tmp)
		return fmt.Errorf("Download unvollständig: %d von %d Bytes", done, resp.ContentLength)
	}
	if err = os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	if progress != nil {
		progress(done, resp.ContentLength)
	}
	return nil
}

func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	base, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for _, z := range r.File {
		target := filepath.Join(dest, filepath.FromSlash(z.Name))
		abs, _ := filepath.Abs(target)
		rel, e := filepath.Rel(base, abs)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("unsicherer ZIP-Pfad: %s", z.Name)
		}
		if z.FileInfo().IsDir() {
			if err = os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		rc, e := z.Open()
		if e != nil {
			return e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if e != nil {
			rc.Close()
			return e
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func locateCLI(root string) string {
	if root == "" {
		return ""
	}
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), "sd-cli.exe") {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
func isModelInstalled() bool {
	files := []struct {
		name string
		min  int64
	}{{"flux-2-klein-4b-q4_0.gguf", 1_800_000_000}, {"Qwen3-4B-Q4_K_S.gguf", 1_800_000_000}, {"flux2-vae.safetensors", 250_000_000}}
	if locateCLI(engineDir) == "" {
		return false
	}
	for _, f := range files {
		st, e := os.Stat(filepath.Join(modelDir, f.name))
		if e != nil || st.Size() < f.min {
			return false
		}
	}
	return true
}

func checkGPU() {
	stateMu.Lock()
	if busy {
		stateMu.Unlock()
		return
	}
	busy = true
	stateMu.Unlock()
	go func() {
		defer func() { stateMu.Lock(); busy = false; stateMu.Unlock() }()
		cli := locateCLI(engineDir)
		if cli == "" {
			setStatus("GPU-Prüfung noch nicht möglich: zuerst Engine installieren.")
			return
		}
		setStatus("Prüfe Vulkan-Geräte …")
		cmd := exec.Command(cli, "--list-devices")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		b, err := cmd.CombinedOutput()
		out := strings.TrimSpace(string(b))
		if out == "" {
			out = "Keine Geräteausgabe erhalten."
		}
		appendLog("Vulkan-Geräteliste:\r\n" + out + "\r\n")
		if err != nil {
			setStatus("Vulkan-Prüfung fehlgeschlagen. Prüfe AMD-Grafiktreiber und Vulkan-Unterstützung.")
			appendLog("Fehler: " + err.Error() + "\r\n")
		} else {
			setStatus("Vulkan-Geräte wurden abgefragt. Prüfe im Protokoll, ob deine AMD Radeon aufgeführt ist.")
		}
	}()
}

func pickImage() {
	filter, _ := syscall.UTF16PtrFromString("Bilddateien\x00*.png;*.jpg;*.jpeg;*.webp;*.bmp\x00Alle Dateien\x00*.*\x00\x00")
	fileBuf := make([]uint16, 32768)
	title, _ := syscall.UTF16PtrFromString("Bild zum Bearbeiten auswählen")
	of := openFileName{StructSize: uint32(unsafe.Sizeof(openFileName{})), HwndOwner: mainHWND, Filter: filter, FilterIndex: 1, File: &fileBuf[0], MaxFile: uint32(len(fileBuf)), Title: title, Flags: ofNFileMustExist | ofNPathMustExist}
	r, _, _ := procOpenFile.Call(uintptr(unsafe.Pointer(&of)))
	if r != 0 {
		selectedImage = syscall.UTF16ToString(fileBuf)
		setText(controls[idPickImage], "Eingabebild ausgewählt")
		setStatus("Bild geladen: " + filepath.Base(selectedImage) + ". Die Generierung bearbeitet dieses Bild.")
		appendLog("Eingabebild: " + selectedImage + "\r\n")
	}
}

func startGenerate() {
	stateMu.Lock()
	if busy {
		stateMu.Unlock()
		return
	}
	if !isModelInstalled() {
		stateMu.Unlock()
		showMessage("Flux Studio", "Bitte zuerst „Engine + Modell installieren“ ausführen.")
		return
	}
	busy = true
	stateMu.Unlock()
	motif := strings.TrimSpace(textOf(idMotif))
	env := strings.TrimSpace(textOf(idEnvironment))
	style := strings.TrimSpace(textOf(idStyle))
	light := strings.TrimSpace(textOf(idLight))
	extra := strings.TrimSpace(textOf(idExtra))
	if motif == "" {
		stateMu.Lock()
		busy = false
		stateMu.Unlock()
		showMessage("Eingabe fehlt", "Bitte gib zuerst das Motiv oder Objekt ein.")
		return
	}
	parts := []string{motif}
	if env != "" {
		parts = append(parts, "in "+env)
	}
	if style != "" {
		parts = append(parts, style)
	}
	if light != "" {
		parts = append(parts, light)
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	prompt := strings.Join(parts, ", ")
	idx, _, _ := procSend.Call(controls[idResolution], cbGetCurSel, 0, 0)
	sizes := [][2]int{{512, 512}, {768, 768}, {1024, 1024}, {1024, 768}, {768, 1024}}
	w, h := 768, 768
	if int(idx) >= 0 && int(idx) < len(sizes) {
		w = sizes[idx][0]
		h = sizes[idx][1]
	}
	img := selectedImage
	go func() {
		defer func() { stateMu.Lock(); busy = false; stateMu.Unlock() }()
		cli := locateCLI(engineDir)
		if cli == "" {
			setStatus("Engine nicht gefunden.")
			return
		}
		outDir := filepath.Join(os.Getenv("USERPROFILE"), "Pictures", "FluxStudio")
		_ = os.MkdirAll(outDir, 0755)
		out := filepath.Join(outDir, "FluxStudio_"+time.Now().Format("20060102_150405")+".png")
		// The backend flag accepts either a backend name (for all modules) or
		// explicit module assignments. "all=vulkan0" is NOT valid syntax.
		// --flash-attn auto uses the engine’s current backend-aware attention selection.
		args := []string{"--diffusion-model", filepath.Join(modelDir, "flux-2-klein-4b-q4_0.gguf"), "--vae", filepath.Join(modelDir, "flux2-vae.safetensors"), "--llm", filepath.Join(modelDir, "Qwen3-4B-Q4_K_S.gguf"), "-p", prompt, "--cfg-scale", "1.0", "--steps", "4", "--sampling-method", "euler", "--flash-attn", "auto", "--offload-to-cpu", "-W", strconv.Itoa(w), "-H", strconv.Itoa(h), "--backend", "vulkan0", "-v", "-o", out}
		if img != "" {
			args = append(args, "-r", img)
		}
		mode := "Bildgenerierung"
		if img != "" {
			mode = "Bildbearbeitung"
		}
		setStatus(mode + " läuft lokal über Vulkan. Erste Ausführung kann mehrere Minuten dauern …")
		appendLog("\r\n" + mode + ": " + prompt + "\r\nAuflösung: " + strconv.Itoa(w) + " x " + strconv.Itoa(h) + "\r\nBackend: Vulkan (alle Modellmodule)\r\nAusgabe: " + out + "\r\n")
		appendLog("Starte lokale Engine mit dokumentierten FLUX.2-Klein-Argumenten …\r\n")
		cmd := exec.Command(cli, args...)
		cmd.Dir = engineDir
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		stdout, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			setStatus("Engine-Ausgabe konnte nicht geöffnet werden.")
			appendLog("FEHLER: " + pipeErr.Error() + "\r\n")
			return
		}
		stderr, pipeErr := cmd.StderrPipe()
		if pipeErr != nil {
			setStatus("Engine-Fehlerausgabe konnte nicht geöffnet werden.")
			appendLog("FEHLER: " + pipeErr.Error() + "\r\n")
			return
		}
		if err := cmd.Start(); err != nil {
			setStatus("Die Bild-Engine konnte nicht gestartet werden.")
			appendLog("FEHLER beim Start von sd-cli.exe: " + err.Error() + "\r\nPrüfe, ob Engine-Dateien und Windows-Laufzeitbibliotheken vorhanden sind.\r\n")
			return
		}
		var readers sync.WaitGroup
		stream := func(r io.Reader, prefix string) {
			defer readers.Done()
			scanner := bufio.NewScanner(r)
			scanner.Buffer(make([]byte, 4096), 2*1024*1024)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line != "" {
					appendLog(prefix + line + "\r\n")
				}
			}
			if e := scanner.Err(); e != nil {
				appendLog("Protokoll-Lesefehler: " + e.Error() + "\r\n")
			}
		}
		readers.Add(2)
		go stream(stdout, "")
		go stream(stderr, "[Engine] ")
		// Drain both pipes before Wait closes them, so progress and errors are not lost.
		readers.Wait()
		err := cmd.Wait()
		if err != nil {
			setStatus("Generierung fehlgeschlagen. Die genaue Fehlermeldung steht jetzt im Protokoll.")
			appendLog("FEHLER: sd-cli.exe wurde mit einem Fehler beendet: " + err.Error() + "\r\n")
			return
		}
		if st, e := os.Stat(out); e != nil || st.Size() < 1000 {
			setStatus("Engine hat keinen Bildausgabepfad erzeugt. Prüfe das Protokoll.")
			return
		}
		lastOutput = out
		setStatus("Fertig. Bild gespeichert: " + out)
		appendLog("Bild erfolgreich gespeichert.\r\n")
		openPath(out)
	}()
}

func openFolder() {
	p := filepath.Join(os.Getenv("USERPROFILE"), "Pictures", "FluxStudio")
	_ = os.MkdirAll(p, 0755)
	openPath(p)
}
func openLastImage() {
	if lastOutput == "" {
		showMessage("Flux Studio", "In dieser Sitzung wurde noch kein Bild erzeugt.")
		return
	}
	openPath(lastOutput)
}
func openPath(path string) {
	verb, _ := syscall.UTF16PtrFromString("open")
	p, _ := syscall.UTF16PtrFromString(path)
	procShellExecute.Call(mainHWND, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(p)), 0, 0, 1)
}

func freeDiskBytes(path string) int64 {
	_ = os.MkdirAll(path, 0755)
	p, _ := syscall.UTF16PtrFromString(path)
	var avail, total, totalFree uint64
	r, _, _ := procDiskSpace.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return -1
	}
	return int64(avail)
}

// Kept for diagnostics in the source package; it verifies model files without
// requiring a separate Python/ROCm installation.
func verifySHA256(path, expected string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return e
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("SHA-256 mismatch: %s", got)
	}
	return nil
}

var _ = verifySHA256
