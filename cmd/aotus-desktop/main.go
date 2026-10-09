//go:build desktop

package main

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"aotus/internal/client"
	"aotus/internal/datadir"
	"aotus/internal/version"
)

// The built frontend (cd frontend && npm run build, or make desktop).
//
//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Typed events for the generated TypeScript bindings.
	application.RegisterEvent[*client.Update](EventUpdate)
	application.RegisterEvent[*client.Approval](EventApproval)
	application.RegisterEvent[TerminalChunk](EventTerminal)
	application.RegisterEvent[DaemonState](EventDaemon)
	application.RegisterEvent[any](EventResync)
}

// Shell is what the window can ask of the app itself, apart from the daemon.
type Shell struct {
	settings *SettingsStore
	keep     func(bool)
}

// Version is the version of the desktop app.
func (s *Shell) Version() string { return version.Version }

// GetSettings returns the preferences of the window.
func (s *Shell) GetSettings() Settings { return s.settings.Load() }

// SaveSettings stores the preferences and applies them now.
func (s *Shell) SaveSettings(v Settings) error {
	if err := s.settings.Save(v); err != nil {
		return err
	}
	s.keep(v.KeepInTray)
	return nil
}

func main() {
	layout, err := datadir.Default()
	if err != nil {
		log.Fatal(err)
	}
	settings, err := NewSettingsStore("")
	if err != nil {
		log.Fatal(err)
	}

	var app *application.App
	backend := NewBackend(Options{Layout: layout, Emit: func(name string, data any) {
		if app != nil {
			app.Event.Emit(name, data)
		}
	}})

	var (
		mu       sync.Mutex
		keepInTr = settings.Load().KeepInTray
		tray     *application.SystemTray
		win      *application.WebviewWindow
	)
	shell := &Shell{settings: settings}

	app = application.New(application.Options{
		Name:        "Aotus",
		Description: "Your AI employees, on your own subscriptions",
		Services: []application.Service{
			application.NewService(backend),
			application.NewService(shell),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:    application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
	})

	showWindow := func() {
		win.Show()
		win.UnMinimise()
		win.Focus()
	}
	// The tray exists only while "keep in the tray" is on: it costs memory.
	setKeep := func(on bool) {
		mu.Lock()
		defer mu.Unlock()
		keepInTr = on
		if on && tray == nil {
			tray = app.SystemTray.New()
			tray.SetIcon(trayIcon())
			tray.SetTooltip("Aotus")
			menu := app.NewMenu()
			menu.Add("Open Aotus").OnClick(func(*application.Context) { showWindow() })
			menu.AddSeparator()
			menu.Add("Quit (employees keep working)").OnClick(func(*application.Context) { app.Quit() })
			tray.SetMenu(menu)
			tray.OnClick(showWindow)
		}
		if !on && tray != nil {
			tray.Destroy()
			tray = nil
		}
	}
	shell.keep = setKeep

	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Aotus",
		Width:            1100,
		Height:           720,
		MinWidth:         720,
		MinHeight:        480,
		BackgroundColour: application.NewRGB(17, 18, 23),
		URL:              "/",
	})
	// Closing the window quits the app unless the user chose to keep it in the
	// tray. Either way the daemon, and every employee, keeps running.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		mu.Lock()
		keep := keepInTr
		mu.Unlock()
		if keep {
			e.Cancel()
			win.Hide()
			return
		}
		backend.shutdown()
		app.Quit()
	})
	if keepInTr {
		setKeep(true)
	}

	if err := app.Run(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

// trayIcon draws a small round icon so the app needs no image file.
func trayIcon() []byte {
	const n = 32
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	c := color.RGBA{R: 0xd9, G: 0x8a, B: 0x3d, A: 0xff}
	for y := range n {
		for x := range n {
			dx, dy := float64(x)-15.5, float64(y)-15.5
			if dx*dx+dy*dy <= 14*14 {
				img.Set(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
