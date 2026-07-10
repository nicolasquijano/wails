//go:build linux && !gtk3 && !cef && !android && !server

package application

func gtkDispatch(fn func()) {
	go func() {
		defer handlePanic()
		fn()
	}()
}
