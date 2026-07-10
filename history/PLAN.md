# Plan de implementación: soporte CEF en Wails v3 (Linux)

**Rama**: `feat/linux-cef`
**Base**: `wailsapp/wails@master` (v3.0.0-alpha2.117, 2026-07-09)
**Objetivo**: Incorporar CEF como motor de render alternativo a WebKitGTK en Linux, manteniendo intactos los backends `webgtk` (default) y `gtk3` (legacy). Sin tocar Windows/macOS/Android/iOS.

---

## 1. Contexto y restricciones

Wails v3 ya tiene una arquitectura multi-backend en Linux basada en **build tags Go** (no runtime switches). El patrón vigente desde la decisión 1.1 (post-issue #5459, 2026-05-16):

| Build tag | Backend | Estado actual |
|---|---|---|
| (default) `!gtk3` | GTK4 + WebKitGTK 6.0 (`application_linux.go`, `linux_cgo.go`) | **Estable, default** |
| `gtk3` | GTK3 + WebKit2GTK 4.1 (`application_linux_gtk3.go`, `linux_cgo_gtk3.go`) | Legacy opt-in, removido en v3.1 |
| `server` | Headless HTTP, sin GUI | Para backends serverless |
| `cef` *(a crear)* | CEF + Chromium Embedded | **Nuevo, opt-in experimental** |

**Restricciones clave:**
- No modificar Windows/macOS/Android/iOS.
- `webgtk` (default) y `gtk3` deben seguir compilando exactamente igual.
- El usuario activa CEF con `go build -tags cef` (mismo estilo que `-tags gtk3`).
- No romper el asset server existente ni el bridge IPC con el JS runtime (`messageprocessor_browser.go`).
- `IMPLEMENTATION.md` es persistente (per AGENTS.md); debe actualizarse con cada commit.

---

## 2. Decisiones arquitectónicas (a validar con el equipo)

### 2.1 Binding Go↔CEF: `energye/energy` vs cgo manual a `libcef.so`

| Aspecto | `energye/energy v1.109.1184` | cgo manual contra `libcef.so` |
|---|---|---|
| Madurez | Producción, varios años, doc en `cefsun` | Nula; hay que escribir los bindings |
| Mantenimiento | Activo (v1.109 alinea con CEF 109) | Total responsabilidad nuestra |
| Dependencia runtime | Carga `libcef.so` enlazada por C (LCL/GTK) | Carga `libcef.so` por dlopen |
| Wrapper gráfico | GTK3 via `golang.org/x/exp/shiny`-estilo LCL | Usamos el `CefBrowserHost::CreateBrowser` con su propio widget parent GTK |
| Peso binario | +~30MB CEF distribución | Igual (CEF binarios aparte) |
| Alineamiento con Wails v3 | Independiente | Independiente |
| Riesgo | LCL podría divergir de GTK4 en uso | Ninguna dependencia extra |
| Esfuerzo | ~1-2 semanas (montar la integración) | ~4-6 semanas (replicar el equivalente de `linux_cgo.go`, ~2200 LOC) |

**Recomendación**: `energye/energy`. La sección crítica a reimplementar (`linux_cgo.go`) tiene 2164 LOC con muchos callbacks C↔Go y bindings a WebKitGTK; replicar eso contra CEF C API en cgo es esfuerzo no rentable para un opt-in.

### 2.2 Tag de build

`cef` (siguiendo la convención `gtk3`).

### 2.3 Cómo exponer la opción al usuario

`wails.json` (futuro, opcional):
```json
{ "build": { "tags": ["cef"] } }
```

Mientras tanto, el flag `-tags cef` es suficiente.

### 2.4 Distribución de binarios CEF

CEF requiere `libcef.so`, `icudtl.dat`, `v8-*.bin`, `chrome-sandbox`, locales, etc. Recomendamos **CMake + `ExternalProject`** desde `https://cef-builds.spotifycdn.com` (CefClient minimal) o paquete nativo del SO donde exista. **Aceptar** que el usuario provea `LD_LIBRARY_PATH` apuntando a los binarios CEF (estilo Wails ya distribuye deps WebKitGTK via sistema).

---

## 3. Inventario de archivos a crear / modificar

### 3.1 Backend principal (capa webview) — crear

| Archivo | LOC est. | Propósito |
|---|---|---|
| `v3/pkg/application/application_linux_cef.go` | ~250 | Lifecycle de la app, init del `CefApp`, integración con `linuxApp`, expose `runCEFMessageLoop`, equivalente a `application_linux.go` |
| `v3/pkg/application/linux_cgo_cef.go` | ~1800 | C-bindings: `CefBrowserHost::CreateBrowser`, `CefClient`, `CefLifeSpanHandler`, `CefLoadHandler`, `CefRenderHandler`, `CefRequestHandler`/`CefResourceRequestHandler` (para el asset server), `CefV8Handler` (para IPC `external`), `CefPermissionHandler`, `CefContextMenuHandler`, `CefDragHandler`, `CefDisplayHandler` (devtools), `CefFocusHandler`, `ExecuteJavaScript`, `ShowDevTools` |
| `v3/pkg/application/webview_window_linux_cef.go` | ~300 | Implementación de `linuxWebviewWindow` cuando hay `cef`, equivalente a `webview_window_linux.go`. Reusa los métodos comunes (setters, bounds, etc) que ya están en el archivo compartido. |
| `v3/pkg/application/cef_init.go` | ~80 | Helper para detección: si el binario fue compilado con `-tags cef`, expone `webviewEngine = "cef"`. |

### 3.2 Archivos a MODIFICAR (build tags, no lógica)

Todos estos archivos tienen `linux && cgo && !gtk3` y necesitan agregar `&& !cef` para que el path default siga compilando con el código CEF desactivado:

```
v3/pkg/application/application_linux.go
v3/pkg/application/linux_cgo.go
v3/pkg/application/webview_window_linux.go            (queda compartido: !gtk3 && !cef ? — ver §5)
v3/pkg/application/gtkdispatch_linux.go
v3/pkg/application/menu_linux.go
v3/pkg/application/menuitem_linux.go
v3/internal/assetserver/webview/request_linux.go
v3/internal/assetserver/webview/responsewriter_linux.go
v3/internal/assetserver/webview/webkit_linux.go
```

Cambio: `linux && cgo && !gtk3 && !android` → `linux && cgo && !gtk3 && !cef && !android`

(Para `webview_window_linux.go` queda `linux && !gtk3 && !cef && !android && !server` para que sólo uno de los tres motores lo provea.)

### 3.3 Archivos a CREAR espejo `*_cef.go` para GTK/UI compartida

Estos archivos hoy tienen `!gtk3` (default GTK4). Con `cef` también necesitamos un GTK host mínimo para crear la `CefWindowInfo` con parent GTK:

```
v3/pkg/application/gtkdispatch_linux_cef.go    (pequeño: reusa gtkdispatch_linux.go)
```

Análisis: como CEF se inicializa con `CefWindowInfo` que apunta a un `GtkWindow*` real, **necesitamos GTK aunque el motor sea CEF**. Esto significa que `cef` arrastra la dependencia de GTK4 como host. Aceptable: el usuario ya tiene GTK4 instalado (lo provee el SO).

### 3.4 Asset server / IPC bridge

CEF necesita su propio equivalente a los handlers en `v3/internal/assetserver/webview/`:

```
v3/internal/assetserver/webview/request_linux_cef.go
v3/internal/assetserver/webview/responsewriter_linux_cef.go
v3/internal/assetserver/webview/cef_scheme_handler.go   (mapea wails://* y http://wails.localhost/*)
```

Estos interceptan las requests CEF en `CefResourceRequestHandler::OnBeforeResourceLoad` y las redirigen al `assetserver.Handler` interno.

### 3.5 IPC bridge JS↔Go

CEF no tiene un "message handler" como WebKit2GTK. Hay que:

1. Inyectar un shim JS en `CefFrame::ExecuteJavaScript` al cargar la página: este shim crea `window.wailsIPC` que habla con `window.cefBridge` (un objeto Go inyectado vía `CefV8Handler`).
2. El `CefV8Handler` Go implementa `window.cefBridge.{call,events}`, llamando al `messageprocessor.go` ya existente.

Archivos:
```
v3/pkg/application/cef_js_shim.js   (inyectado al cargar)
v3/internal/runtime/cef_bridge.go   (CefV8Handler Go)
```

### 3.6 Devtools

`CefBrowserHost::ShowDevTools(windowInfo, client, settings, inspect_element_at)` abre la ventana DevTools nativa de Chromium. Sin código adicional más allá del binding.

### 3.7 `doctor-ng` y CLI

Agregar `cef` como categoría de dependencias en:
```
v3/pkg/doctor-ng/platform_linux.go
v3/pkg/doctor-ng/packagemanager/apt.go
v3/pkg/doctor-ng/packagemanager/dnf.go
v3/pkg/doctor-ng/packagemanager/pacman.go
v3/pkg/doctor-ng/packagemanager/emerge.go
v3/pkg/doctor-ng/packagemanager/eopkg.go
v3/pkg/doctor-ng/packagemanager/zypper.go
v3/pkg/doctor-ng/packagemanager/nixpkgs.go
v3/pkg/doctor-ng/packagemanager/xbps.go
v3/pkg/doctor-ng/packagemanager/packagemanager.go
```

Lógica: solo validar CEF si `-tags cef` está activo (chequear `runtime.CEFEnabled()` o via debug.ReadBuildInfo()).

### 3.8 Docker build

```
v3/test/docker/Dockerfile.linux-x86_64          (agregar línea CEF)
v3/test/docker/Dockerfile.linux-arm64           (agregar línea CEF)
```

Para CEF en contenedor: descargar el tarball desde `https://cef-builds.spotifycdn.com/cef_binary_109.4.27+gb…` y descomprimir a `/opt/cef`. Variables de entorno en el Dockerfile: `CEF_PATH=/opt/cef`.

### 3.9 Taskfile (CI / build helpers)

```
v3/Taskfile.yaml:
  - test:example:linux:cef        (nuevo, análogo a test:example:linux:gtk3)
  - test:examples:linux:docker:x86_64:cef
  - test:examples:linux:docker:arm64:cef
  - sanity:cef                    (chequeo de compilación)
```

### 3.10 Documentación

- `v3/docs/guides/cef.md` (guía de uso: instalar CEF, ejemplo, troubleshooting).
- `IMPLEMENTATION.md` (raíz): actualizar con cada fase (es persistente, per AGENTS.md).
- `CHANGELOG.md`: entrada por fase.

---

## 4. Fases de implementación

### Fase 0 — Infraestructura (1 día)
**Objetivo**: Rama limpia, scaffolding, build tags en su lugar.

1. Rama `feat/linux-cef` ya creada desde `master`.
2. Modificar los 10 archivos `*_linux*.go` listados en §3.2 agregando `&& !cef` a los build tags.
3. Verificar que `go build` (default) y `go build -tags gtk3` siguen compilando idénticos.
4. CI: agregar `task sanity:cef` que corre `cd v3/examples/plain && go build -tags cef -o /dev/null .` (con stub mínimo primero; full en fase 3).

**Entregable**: build tags aplicados, rama compila en los tres modos (default, gtk3, server). Tag `cef` falla con error de "undefined: linuxWebviewWindow.cefCreate" o similar — esperado.

### Fase 1 — Stub mínimo con `energye/energy` (2-3 días)
**Objetivo**: tener un `webview_window_linux_cef.go` que cree un `CefBrowser` cargando `http://localhost:34115` (dev server) o `wails://` (asset server stub), sin IPC, sin devtools.

1. Agregar `github.com/energye/energy v1.109.1184` como dependencia en `v3/go.mod`.
2. En `application_linux_cef.go`: inicializar `cefapi.LoadLibs(CEF_PATH)` y arrancar el message loop.
3. En `webview_window_linux_cef.go`: usar `cefBrowserWindow` de energye como motor, embedido en un `GtkBox` GTK4.
4. Sin esquema handler propio: usar `custom-scheme` o `http://wails.localhost` con un stub.
5. Verificar: `cd v3/examples/plain && go build -tags cef -o testbuild-plain-cef && ./testbuild-plain-cef` muestra una ventana con un HTML estático.

**Entregable**: ventana GTK4 + CEF cargando una URL, sin IPC.

### Fase 2 — Asset server (3-5 días)
**Objetivo**: servir `wails://*` y `http://wails.localhost/*` desde el `assetserver.Handler` existente.

1. Implementar `CefResourceRequestHandler::OnBeforeResourceLoad`:
   - Matchear scheme `wails` o host `wails.localhost` → rutear a `assetserver.Handler`.
   - Mapear response → `CefResponse` y `CefCallback::Continue()`.
2. `request_linux_cef.go`: extraer path/method/headers del `CefRequest` CEF.
3. `responsewriter_linux_cef.go`: implementar `http.ResponseWriter` que vuelca a `CefResponse`.
4. Implementar `OnResourceLoadComplete` para observabilidad/logs.
5. Verificar: app sirve archivos del frontend desde `wails://` directamente. Test con `examples/plain/frontend/dist`.

**Entregable**: navegador carga el frontend empaquetado desde el asset server.

### Fase 3 — IPC JS↔Go (3-5 días)
**Objetivo**: el runtime de Wails (`messageprocessor_browser.go`) habla con el JS del frontend sobre CEF.

1. Inyectar shim JS (`cef_js_shim.js`) en cada frame via `CefFrame::ExecuteJavaScript` cuando se carga.
   - Crea `window.wails` con la API que el frontend espera (consulta a la versión actual de `frontend/src/runtime/`).
   - Implementa `window.wails.EventsOn`, `window.wails.EventsEmit`, etc. usando `window.cefBridge`.
2. `internal/runtime/cef_bridge.go`:
   - `CefV8Handler::Execute` recibe las llamadas JS a `window.cefBridge.call(method, args)`.
   - Rutea al `messageprocessor.go` existente (sin tocar `messageprocessor_browser.go`).
3. Salida Go → JS: `messageprocessor` emite, el bridge usa `CefFrame::ExecuteJavaScript` para invocar callbacks registrados.
4. Verificar con `examples/events`: el evento `events.Emit("click", data)` aparece en la consola del frontend.

**Entregable**: bidirectional JS↔Go funcional, análogo a webgtk.

### Fase 4 — Devtools + permissions + DnD + menú contextual (3-4 días)
**Objetivo**: paridad funcional con `linux_cgo.go`.

1. `CefBrowserHost::ShowDevTools` para `WebviewWindow.OpenDevToolsWindow()`.
2. `CefPermissionHandler` para `geolocation`, `notifications`, `media`.
3. `CefContextMenuHandler::OnBeforeContextMenu` para menú nativo (reusar `linuxMenu`).
4. Drag & drop: `CefDragHandler` + `CefRenderHandler::StartDragging`.
5. `CefRequestHandler::OnBeforeBrowse` para interceptar navegación y `OpenExternalLink` policy.

**Entregable**: paridad con webgtk en features desktop estándar.

### Fase 5 — `doctor-ng` y packaging (2 días)
**Objetivo**: que `wails3 doctor` sepa qué CEF requiere y cómo instalarlo.

1. Agregar categoría `cef` en `categorizeLinuxDep()`.
2. Agregar `libcef`, `cef-runtime` etc. en cada `packagemanager/*_linux.go` (apt: `libcef-dev` no existe — CEF se descarga binario; ofrecer una URL).
3. `wails3 doctor --engine=cef` muestra versión de CEF detectada por `cefapi.LoadLibs`.
4. Update `v3/test/docker/Dockerfile.linux-*` con `ARG CEF_VERSION=109.4.27` y `wget … | tar -C /opt`.

**Entregable**: `wails3 doctor` detecta estado de CEF.

### Fase 6 — Ejemplos + CI + docs (2 días)
**Objetivo**: cobertura.

1. `v3/examples/cef-hello/` — minimal CEF app.
2. `v3/examples/cef-events/` — IPC bidirectional.
3. `Taskfile.yaml`: tasks `test:example:linux:cef`, `test:examples:linux:docker:x86_64:cef`.
4. `v3/docs/guides/cef.md` con instrucciones de uso, troubleshooting, matriz de soporte.
5. CI workflow: job `linux-cef` que corre `task test:examples:linux:docker:x86_64:cef`.

**Entregable**: PR listo con CI verde.

---

## 5. Riesgos y mitigaciones

| Riesgo | Impacto | Mitigación |
|---|---|---|
| `energye/energy` cae en abandono | Alto (binding único) | Pin a `v1.109.1184`, fork interno como respaldo en `Wails-CEF/internal/energy-fork/` si es necesario |
| CEF requiere X11/Wayland específicos | Medio | Documentar pre-requisitos; probar con `cefapp --ozone-platform=wayland` |
| Tamaño binario +50MB CEF | Bajo (opt-in) | Documentar; ofrecer build `cef-min` con `strip` |
| Diferencias de comportamiento entre WebKit y Chromium (CSS, APIs) | Alto | No prometer paridad 100%; tests de smoke por feature |
| `cef` colisiona con `gtk3` si el usuario combina tags | Bajo | Documentar: `cef` y `gtk3` son mutuamente excluyentes; elegir uno |
| LCL/GTK4 integration en `energye` | Medio | energye v1.109 usa GTK3 internamente — confirmado que con `-tags cef` cargamos `libwebkit2gtk-4.1` igual o cambiamos por CEF. Verificar en fase 1. |

---

## 6. Cómo probar

```bash
# 1. Verificar que webgtk default sigue intacto
cd v3/examples/plain && go build -o /tmp/plain && /tmp/plain

# 2. Verificar gtk3 legacy sigue intacto
cd v3/examples/plain && go build -tags gtk3 -o /tmp/plain-gtk3 && /tmp/plain-gtk3

# 3. Verificar server sigue intacto
cd v3/examples/server && go build -tags server -o /tmp/server && /tmp/server

# 4. Build CEF (requiere libcef.so en LD_LIBRARY_PATH)
cd v3/examples/cef-hello
CEF_PATH=/opt/cef go build -tags cef -o /tmp/cef-hello
LD_LIBRARY_PATH=/opt/cef:$LD_LIBRARY_PATH /tmp/cef-hello

# 5. Test todos los ejemplos CEF
cd v3 && task test:examples:linux:docker:x86_64:cef
```

---

## 7. Criterios de aceptación (para cerrar la fase 6)

- [ ] `go build` (default) y `go build -tags gtk3` y `go build -tags server` sin cambios respecto a `master`.
- [ ] `go build -tags cef` produce un binario que arranca ventana GTK4 con contenido CEF.
- [ ] `examples/cef-events`: emitir evento Go → recibido en JS.
- [ ] `examples/cef-events`: emitir evento JS → recibido en Go.
- [ ] Devtools funciona (F12 o desde menú).
- [ ] `wails3 doctor` muestra categoría `cef` con versión.
- [ ] CI verde en `linux-cef` job (Docker).
- [ ] `IMPLEMENTATION.md` y `CHANGELOG.md` actualizados por fase.
- [ ] Commit firmado, push a `feat/linux-cef` en el fork `Wails-CEF`.

---

## 8. Estructura del directorio `Wails-CEF`

```
Wails-CEF/
├── v3/                                  ← fork de wails v3
│   ├── pkg/application/
│   │   ├── application_linux.go         (modificado: !gtk3 && !cef)
│   │   ├── application_linux_cef.go     (NUEVO)
│   │   ├── application_linux_gtk3.go
│   │   ├── linux_cgo.go                 (modificado: !gtk3 && !cef)
│   │   ├── linux_cgo_cef.go             (NUEVO)
│   │   ├── linux_cgo_gtk3.go
│   │   ├── webview_window_linux.go      (modificado: !gtk3 && !cef en algunos setters)
│   │   └── webview_window_linux_cef.go  (NUEVO)
│   ├── internal/assetserver/webview/
│   │   ├── request_linux.go             (modificado)
│   │   ├── request_linux_cef.go         (NUEVO)
│   │   ├── responsewriter_linux.go      (modificado)
│   │   ├── responsewriter_linux_cef.go  (NUEVO)
│   │   └── cef_scheme_handler.go        (NUEVO)
│   ├── internal/runtime/cef_bridge.go   (NUEVO)
│   ├── pkg/application/cef_js_shim.js   (NUEVO)
│   ├── pkg/doctor-ng/packagemanager/    (modificado: categoría cef)
│   ├── test/docker/Dockerfile.linux-*   (modificado: agregar CEF)
│   ├── Taskfile.yaml                    (modificado: tasks cef)
│   └── examples/
│       └── cef-hello/                   (NUEVO)
├── history/PLAN.md                      (este archivo)
├── IMPLEMENTATION.md                    (tracker persistente, raíz)
└── AGENTS.md                            (del repo wails)
```

---

## 9. Próximos pasos inmediatos

1. Hacer commit del PLAN.md + IMPLEMENTATION.md actualizado.
2. Iniciar Fase 0: modificación de build tags (10 archivos).
3. Validar `go build` en los tres modos sigue funcionando.
4. Crear issues en `bd` para cada fase (§10).

## 10. Issues sugeridos para `bd`

```bash
bd create "Fase 0: scaffolding CEF build tags" -t task -p 1 --json
bd create "Fase 1: stub energye+CEF cargando HTML" -t feature -p 1 --json
bd create "Fase 2: asset server sobre CefResourceRequestHandler" -t feature -p 1 --json
bd create "Fase 3: IPC JS↔Go con CefV8Handler" -t feature -p 1 --deps discovered-from:<fase1> --json
bd create "Fase 4: devtools/permisos/DnD/menu contextual" -t task -p 2 --json
bd create "Fase 5: doctor-ng soporta categoría cef" -t task -p 2 --json
bd create "Fase 6: ejemplos + CI + docs" -t task -p 2 --json
bd create "Spike: validar energye v1.109 con GTK4" -t task -p 1 --json
```