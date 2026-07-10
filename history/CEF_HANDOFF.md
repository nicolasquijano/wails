# CEF Linux handoff

Actualizado: 2026-07-10 (sesión C.0j — CEF 147 pipeline end-to-end)

## Punto de partida

- Rama: `feat/linux-cef`
- Último commit local: `161c28266 fix(v3/linux): unblock CEF 147 end-to-end pipeline`
- Estado funcional: las 4 variantes (`default`, `gtk3`, `server`, `cef`) compilan
  y los tests de `pkg/application` pasan. CEF 147 levanta el navegador, lo
  reparenta al `GtkBox` y el assetserver sirve `wails://localhost/`.
  Render visual pendiente por un `MatchError` de X11 (ver más abajo).

## Resumen de cambios de la sesión C.0j

1. **`gdk_x11_surface_get_xid` devolvía 0** para el `GtkBox` (los widgets
   hijos en GTK4 no tienen superficie nativa). Se usa el XID del
   `GtkApplicationWindow` top-level. El reparent posterior sigue
   funcionando porque `cef_attach_to_gtk_widget` camina
   `widget → GtkNative → surface → XID`.
2. **GDK ignoraba `GDK_BACKEND=x11` en Wayland** — se setea en `init()`,
   se quita `WAYLAND_DISPLAY` y se llama `gtk_init()` antes de
   `cef.Init` para que la display X11 ya esté abierta.
3. **`RuntimeStyleAlloy`** — CEF 147 cambió a runtime "Chrome" que
   ignora `WindowInfo.ParentWindow`. Vuelve a funcionar con Alloy.
4. **Se re-habilita el reparent a `GtkBox`** — el `MatchError` de X11
   visuales obliga a dejar que CEF cree una ventana top-level y
   reparentar después. Confirmado: el log de CEF ya no muestra X
   errors.
5. **GPU process mata el navegador** — `cef.App` instala
   `--disable-gpu --in-process-gpu --ozone-platform=x11
   --runtime-style=alloy`. `OZONE_PLATFORM=x11` se setea también en
   `init()`.
6. **Detección de subprocesos rota** — el `init()` previo hacía
   `os.Exit(0)` antes de que CEF pudiese registrar el subproceso.
   Ahora delega en `cef.MaybeExitSubprocess()`.
7. **GTK main loop hambriento** — instalado `g_idle_add_full` que
   llama `cef.DoMessageLoopWork()` en cada iteración idle.
8. **Stubs no-op** — `setSize` / `setDefaultSize` ahora aplican
   `gtk_window_set_default_size` y `gtk_widget_set_size_request`.

## Verificación ejecutada en esta sesión

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

Smoke test CEF 147:
```
$ DISPLAY=:1 GDK_BACKEND=x11 CEF_DIR=~/cef147std/.../Release /tmp/cef-hello
[cefInit] post-init default display backend=:1
[cefCreateBrowserInWidget] url="wails://localhost/" gtkWindowXID=...
[cefCreateBrowserInWidget] returned browser=true
[cefCreateBrowserInWidget] browser view XID=...
[OnBeforeResourceLoad] url="wails://localhost/" isAsset=true
[linuxWebviewWindow.run] after show
```

`/tmp/wails-cef.log` solo contiene warnings (NVIDIA vaapi, OAuth client
ID, user-type filter) — ningún error. `_NET_CLIENT_LIST` registra la
ventana GTK.

## Procedimiento de continuación

1. **Resolver el `MatchError` de X11 visuales** — CEF (con
   `--ozone-platform=x11`) usa el visual default del X display
   (24-bit TrueColor). GTK4 en KDE Wayland elige un visual ARGB32
   vía `_NET_VISIBLE`. Las dos no comparten visual y
   `XCreateWindow` falla con `Match`.
   - Opciones a investigar:
     - Forzar a GTK a usar el visual default (`GDK_VISUALS=0x23` o
       `gtk_widget_set_visual`)
     - Pasar el visual de GTK a CEF vía `CefWindowInfo.runtime_visual`
     - Usar `SetAsWindowless` con rendering por software puro
2. **Verificación visual en sesión X11 pura** — el screen capture
   tooling (`xwininfo`, `ffmpeg -f x11grab`) devuelve negro en este
   host KDE+XWayland. Confirmar visualmente en un display real.
3. **Auto-resize del CEF view con el `GtkBox`** — completado en
   `443240298`. Conecta `notify::width` y `notify::height` del
   `GtkBox` (GTK4 reemplazó el signal `size-allocate` de GTK3) y
   llama `XResizeWindow` desde el handler en C. El primer allocation
   post-`gtk_window_present` redimensiona la vista CEF al tamaño
   real del box, no al 800×600 inicial.
4. **Workflow de CI para `-tags cef`** — diferido hasta que la ruta
   de rendering esté sólida.
5. **Push a `origin/feat/linux-cef`** — bloqueado por credenciales
   en este entorno. La descripción del PR está en
   `history/PR_CEF.md`.

## Alcance y decisiones vigentes

- Conservar `purego-cef`; no migrar a `energye/energy` sin una
  necesidad API comprobada.
- El backend CEF depende de X11 para adjuntar la vista CEF a la
  ventana GTK4. Verificar explícitamente la sesión de escritorio
  usada durante el smoke test.
- CEF 147 con `--disable-gpu --in-process-gpu` es la línea base
  hasta que encontremos una receta para arrancar el proceso GPU en
  XWayland. Documentado en el `cef.App` instalado por
  `cef_app_stub.go`.

## Estado de herramientas y publicación

- `bd` y `coderabbit` no están instalados en este entorno.
- El remoto `origin` apunta a `https://github.com/wailsapp/wails.git`,
  pero `git push -u origin feat/linux-cef` falló por falta de
  credenciales.
- Antes de publicar: confirmar la base remota, commitear (hecho:
  `161c28266`), rebasear y empujar la rama.
