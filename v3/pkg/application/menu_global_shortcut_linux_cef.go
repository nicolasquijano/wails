//go:build linux && cef && !android && !server

package application

// Phase 1 stubs for the CEF build. These satisfy the platform interfaces
// declared in menu.go, global_shortcut_manager.go, etc. so the package
// compiles when -tags cef is set. Real implementations land in Phase 4
// (which will use CEF menu APIs and Linux portal/X11 for global shortcuts).

// GlobalShortcut is the CEF build's placeholder for the type normally
// defined in global_shortcut_linux.go (which is excluded from -tags cef).
// Phase 4 will replace this with a real X11/portal implementation.
type GlobalShortcut struct {
	ID         string
	Accelerator string
	Callback   func()
}

// --- Menu stubs -----------------------------------------------------------

func newMenuImpl(menu *Menu) *linuxMenu {
	return &linuxMenu{menu: menu}
}

type linuxMenu struct {
	menu *Menu
}

func (m *linuxMenu) update()            {}
func (m *linuxMenu) show(_ any) error    { return nil }
func (m *linuxMenu) hide()               {}
func (m *linuxMenu) nativeMenu() any     { return nil }
func (m *linuxMenu) attachToWindow(_ any) {}
func (m *linuxMenu) processKeyEvent(_ uint) bool { return false }
func (m *linuxMenu) destroy()            {}

// --- Global shortcut stubs ------------------------------------------------

func newGlobalShortcutImpl(manager *GlobalShortcutManager) globalShortcutImpl {
	return &globalShortcutImplCEF{}
}

type globalShortcutImplCEF struct{}

func (g *globalShortcutImplCEF) register(_ int, _ *accelerator) error { return nil }
func (g *globalShortcutImplCEF) unregister(_ int) error                { return nil }
func (g *globalShortcutImplCEF) unregisterAll() error                  { return nil }

// --- Misc helpers ---------------------------------------------------------

// mousePosition is a Phase 1 no-op stub. Real implementation in Phase 4
// uses XQueryPointer / GdkDisplay for global cursor position.
func mousePosition() (x, y int) {
	return 0, 0
}

// newSystemTrayImpl is a Phase 1 stub. Real CEF-aware tray will land in
// Phase 4 using the org.kde.StatusNotifierItem D-Bus interface.
func newSystemTrayImpl(s *SystemTray) systemTrayImpl {
	return &systemTrayImplCEF{tray: s}
}

type systemTrayImplCEF struct {
	tray *SystemTray
}

func (t *systemTrayImplCEF) Show()                              {}
func (t *systemTrayImplCEF) Hide()                              {}
func (t *systemTrayImplCEF) run()                               {}
func (t *systemTrayImplCEF) destroy()                           {}
func (t *systemTrayImplCEF) setLabel(_ string)                  {}
func (t *systemTrayImplCEF) setTooltip(_ string)                {}
func (t *systemTrayImplCEF) setIcon(_ []byte)                   {}
func (t *systemTrayImplCEF) setTemplateIcon(_ []byte)           {}
func (t *systemTrayImplCEF) setDarkModeIcon(_ []byte)           {}
func (t *systemTrayImplCEF) setMenu(_ *Menu)                    {}
func (t *systemTrayImplCEF) setIconPosition(_ IconPosition)     {}
func (t *systemTrayImplCEF) bounds() (*Rect, error)             { return &Rect{}, nil }
func (t *systemTrayImplCEF) getScreen() (*Screen, error)        { return nil, nil }
func (t *systemTrayImplCEF) positionWindow(_ Window, _ int) error { return nil }
func (t *systemTrayImplCEF) openMenu()                          {}