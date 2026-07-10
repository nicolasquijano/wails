# CEF Linux handoff

Actualizado: 2026-07-10 (sesión C.0j — smoke test CEF 147)

## Punto de partida

- Rama: `feat/linux-cef`
- Último commit local: `8a48f73ba docs: add CEF continuation handoff`
- Estado funcional: las 4 variantes (`default`, `gtk3`, `server`, `cef`) compilan
  y los tests de `pkg/application` pasan. CEF 147 levanta el navegador y sirve
  `wails://localhost/` desde el assetserver.

## Resultado del smoke test CEF 147 (sesión C.0j)

Se construyó y ejecutó `v3/examples/cef-hello` contra CEF 147.14
(`cef_binary_147.0.14+g76d2442+chromium-147.0.7727.138_linux64`) en una
sesión KDE Wayland con `GDK_BACKEND=x11`. La ventana se abre y el
navegador carga el HTML del assetserver (`OnBeforeResourceLoad
url="wails://localhost/" isAsset=true`).

### Lo que se arregló en esta sesión

1. **`gdk_x11_surface_get_xid` devolvía 0** — los widgets hijos en
   GTK4 no tienen superficie nativa. Se pasó el `GtkApplicationWindow`
   completo a `cefCreateBrowserInWidget` (en lugar del `GtkBox`) y se
   leyó el XID de la ventana top-level.
2. **GDK forzando Wayland** — la sesión Wayland hacía que GTK ignorase
   `GDK_BACKEND=x11`. El `init()` ahora ajusta `GDK_BACKEND=x11` y
   quita `WAYLAND_DISPLAY` antes de cualquier llamada a GTK/CEF.
3. **XID=1 (root) para la vista CEF** — CEF 147 cambió a runtime
   "Chrome" que no respeta `WindowInfo.ParentWindow`. Forzado
   `RuntimeStyleAlloy` para volver al comportamiento legacy.
4. **`XReparentWindow` con `BadWindow`** — la conexión X de CEF no es
   intercambiable con la de GDK. Se eliminó el reparent; la creación
   del navegador ya anida la ventana CEF dentro del `GtkWindow`.
5. **GPU process mata el navegador** — `cef.App` instalado con
   `OnBeforeCommandLineProcessing` que añade `--disable-gpu`,
   `--disable-software-rasterizer`, `--in-process-gpu`,
   `--runtime-style=alloy`.
6. **Detección de subprocesos rota** — el `init()` previo hacía
   `os.Exit(0)` antes de que CEF pudiese registrar el subproceso. Ahora
   delega en `cef.MaybeExitSubprocess()`.
7. **GTK main loop hambriento** — instalado `g_idle_add_full` que llama
   `cef.DoMessageLoopWork()` en cada iteración idle.
8. **Stubs no-op** — `setSize` / `setDefaultSize` ahora aplican
   `gtk_window_set_default_size` y `gtk_widget_set_size_request`.

## Procedimiento de continuación

1. Verificar visualmente que la ventana aparece con el HTML de
   `wails://localhost/`. El log de debug en `/tmp/wails-cef-debug.log`
   debe terminar en:
   ```
   [cefCreateBrowserInWidget] returned browser=true
   [OnBeforeResourceLoad] url="wails://localhost/" isAsset=true
   [linuxWebviewWindow.run] after show
   ```
2. Probar el botón de IPC, eventos Go→JS, resize, cierre y una
   segunda ventana (Phase 4 sigue pendiente de verificación
   end-to-end visual).
3. Cuando esté confirmado, commitear los cambios de esta sesión con
   `docs: update implementation tracker for phase 4.7 — CEF 147
   smoke test pass`.

## Validación ejecutada en esta sesión

```bash
cd v3
go build -tags cef -o /tmp/cef-hello .              # examples/cef-hello  exit 0
go test -tags cef ./pkg/application                  # ok
go test            ./pkg/application                  # ok
go test -tags gtk3 ./pkg/application                  # ok

for tags in '' gtk3 server cef; do
  if [ -n "$tags" ]; then
    (cd examples/plain && go build -tags "$tags" -o "/tmp/wails-$tags" .)
  else
    (cd examples/plain && go build -o /tmp/wails-default .)
  fi
done                                                  # los 4 binarios compilan
```

Los warnings de X11 deprecado son conocidos y se dejan tal cual.

## Alcance y decisiones vigentes

- Conservar `purego-cef`; no migrar a `energye/energy` sin una necesidad API
  comprobada.
- El backend CEF depende de X11 para adjuntar la vista CEF a la ventana
  GTK4. Verificar explícitamente la sesión de escritorio usada durante el
  smoke test.
- CEF 147 con `--disable-gpu` es la línea base hasta que encontremos
  una receta para arrancar el proceso GPU en XWayland. Documentado en
  el `cef.App` instalado por `cef_app_stub.go`.

## Estado de herramientas y publicación

- `bd` y `coderabbit` no están instalados en este entorno.
- El remoto `origin` apunta a `https://github.com/wailsapp/wails.git`, pero
  `git push -u origin feat/linux-cef` falló por falta de credenciales.
- Antes de publicar: confirmar la base remota, actualizar
  `IMPLEMENTATION.md` (hecho), commitear, rebasear y empujar la rama.
