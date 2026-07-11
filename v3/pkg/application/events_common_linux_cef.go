//go:build linux && cgo && cef && !android && !server

package application

/*
#include <stdint.h>
*/
import "C"

import (
	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// processWindowEvent is the CEF build's C-callable forwarder for window
// events. The WebKit backend defines this in linux_cgo.go (excluded
// from -tags cef); CEF needs its own because the focus controllers
// in linux_cgo_cef.go's CGo preamble call it via a static C callback.
//
// The implementation is intentionally trivial: write a windowEvent to
// the package-level windowEvents channel that application.go drains
// on a goroutine.
//
// //export processWindowEvent makes the Go symbol visible to the C
// linker so the static C callbacks (cef_focus_enter_cb / _leave_cb)
// can call it. The arguments are C.uint (matching C's `unsigned int`
// on Linux x86_64 and the other backends' signatures) so the linker
// can match the C declarations in the cgo preamble.
//
//export processWindowEvent
func processWindowEvent(windowID C.uint, eventID C.uint) {
	windowEvents <- &windowEvent{
		WindowID: uint(windowID),
		EventID:  uint(eventID),
	}
}

// commonApplicationEventMapCEF mirrors the WebKit backend's
// events_common_linux.go but is selected when -tags cef is set. The
// runtime.js Common.* event surface (Common.ApplicationStarted,
// Common.SystemWillSleep, Common.SystemDidWake,
// Common.ThemeChanged) lets app code stay portable across platforms.
//
// Linux.* events are emitted from the platform-specific sources below
// (CEF OnLoadEnd, GTK focus signals, dbus logind PrepareForSleep, the
// xdg-desktop portal Settings portal); we forward each one to its
// Common.* counterpart here.
var commonApplicationEventMapCEF = map[events.ApplicationEventType]events.ApplicationEventType{
	events.Linux.ApplicationStartup: events.Common.ApplicationStarted,
	events.Linux.SystemThemeChanged: events.Common.ThemeChanged,
	events.Linux.SystemWillSleep:    events.Common.SystemWillSleep,
	events.Linux.SystemDidWake:      events.Common.SystemDidWake,
}

// setupCommonEvents forwards each Linux platform event to its Common.*
// counterpart so apps subscribing via application.Events.OnApplicationEvent
// receive Common.ApplicationStarted, etc. — the same surface every
// other Wails backend exposes. Called once during linuxApp.run.
func (a *linuxApp) setupCommonEvents() {
	for sourceEvent, targetEvent := range commonApplicationEventMapCEF {
		sourceEvent := sourceEvent
		targetEvent := targetEvent
		a.parent.Event.OnApplicationEvent(sourceEvent, func(event *ApplicationEvent) {
			applicationEvents <- &ApplicationEvent{
				Id:  uint(targetEvent),
				ctx: event.ctx,
			}
		})
	}
}

// monitorPowerEventsCEF subscribes to systemd-logind's PrepareForSleep
// signal on the system bus and translates it into
// events.Linux.SystemWillSleep (arg=true, just before suspend) and
// events.Linux.SystemDidWake (arg=false, immediately on resume).
//
// On systems without systemd / logind / elogind reachable on the
// system bus (Alpine, Void, some Devuan setups), we log a warning and
// exit cleanly so the rest of the app keeps working. Without the
// NameHasOwner probe, AddMatchSignal would succeed on any systemd-less
// distro and the goroutine would block forever on a channel that never
// receives — silently masking the missing service.
func (a *linuxApp) monitorPowerEventsCEF() {
	go func() {
		defer handlePanic()
		conn, err := dbus.ConnectSystemBus()
		if err != nil {
			a.parent.warning("[WARNING] failed to connect to system bus; sleep/wake events will not fire: %v", err)
			return
		}
		defer conn.Close()

		var hasOwner bool
		if err := conn.BusObject().Call(
			"org.freedesktop.DBus.NameHasOwner", 0, "org.freedesktop.login1",
		).Store(&hasOwner); err != nil {
			a.parent.warning("[WARNING] failed to probe org.freedesktop.login1; sleep/wake events will not fire: %v", err)
			return
		}
		if !hasOwner {
			a.parent.warning("[WARNING] systemd-logind/elogind not reachable on the system bus; sleep/wake events will not fire")
			return
		}

		// Constrain the sender to logind's well-known name so a
		// hostile connection on the system bus can't spoof
		// PrepareForSleep signals.
		if err = conn.AddMatchSignal(
			dbus.WithMatchSender("org.freedesktop.login1"),
			dbus.WithMatchInterface("org.freedesktop.login1.Manager"),
			dbus.WithMatchMember("PrepareForSleep"),
			dbus.WithMatchObjectPath("/org/freedesktop/login1"),
		); err != nil {
			a.parent.warning("[WARNING] failed to subscribe to logind PrepareForSleep; sleep/wake events will not fire: %v", err)
			return
		}

		c := make(chan *dbus.Signal, 4)
		conn.Signal(c)

		for v := range c {
			if v.Name != "org.freedesktop.login1.Manager.PrepareForSleep" {
				continue
			}
			if len(v.Body) < 1 {
				continue
			}
			willSleep, ok := v.Body[0].(bool)
			if !ok {
				continue
			}
			if willSleep {
				applicationEvents <- newApplicationEvent(events.Linux.SystemWillSleep)
			} else {
				applicationEvents <- newApplicationEvent(events.Linux.SystemDidWake)
			}
		}
	}()
}

// listenForSystemThemeChangesCEF watches
// org.freedesktop.appearance::color-scheme on the session bus via the
// xdg-desktop portal Settings interface. Whenever the user toggles
// light/dark mode, we push events.Linux.SystemThemeChanged onto the
// applicationEvents channel; setupCommonEvents forwards that to
// events.Common.ThemeChanged for portable subscriptions.
//
// Lives in its own goroutine and exits cleanly if the session bus or
// the portal interface isn't available (rare on desktop distros but
// possible in minimal containers).
func (a *linuxApp) listenForSystemThemeChangesCEF() {
	go func() {
		defer handlePanic()
		conn, err := dbus.SessionBus()
		if err != nil {
			a.parent.warning("[WARNING] failed to connect to session bus; theme-change events will not fire: %v", err)
			return
		}
		defer conn.Close()

		if err = conn.AddMatchSignal(
			dbus.WithMatchInterface("org.freedesktop.portal.Settings"),
			dbus.WithMatchMember("SettingChanged"),
		); err != nil {
			a.parent.warning("[WARNING] failed to subscribe to portal SettingChanged; theme-change events will not fire: %v", err)
			return
		}

		c := make(chan *dbus.Signal, 10)
		conn.Signal(c)

		for s := range c {
			if len(s.Body) < 3 {
				continue
			}
			namespace, ok := s.Body[0].(string)
			if !ok || namespace != "org.freedesktop.appearance" {
				continue
			}
			key, ok := s.Body[1].(string)
			if !ok || key != "color-scheme" {
				continue
			}
			a.theme = "system"
			applicationEvents <- newApplicationEvent(events.Linux.SystemThemeChanged)
		}
	}()
}