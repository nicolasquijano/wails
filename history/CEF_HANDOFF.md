# CEF Linux handoff

Actualizado: 2026-07-10

## Punto de partida

- Rama: `feat/linux-cef`
- Último commit local: `effc39aae fix(v3/linux): dispatch CEF windows on GTK main loop`
- Árbol de trabajo: limpio al crear este documento.
- Estado funcional: las variantes default, `gtk3`, `server` y `cef` compilan. CEF sigue siendo experimental hasta completar una ejecución con CEF 147 o superior.

La corrección más reciente reemplazó el falso dispatcher CEF por una cola
`g_idle_add_full` al contexto principal de GLib. Las ventanas pendientes se
crean ahora en el hilo que ejecuta `g_application_run`, en vez de hacerlo en
una goroutine cualquiera. También se corrigió el manejo de fallo de
`cef.Init`: un runtime incompatible devuelve el error sin crear objetos V8 ni
entrar en panic.

## Siguiente objetivo

Ejecutar `v3/examples/cef-hello` con un runtime CEF 147+ y comprobar que el
navegador se crea, muestra el HTML servido por `wails://localhost/` y no queda
en una ventana negra o gris.

El binario de Steam disponible en esta máquina es CEF 126 y no sirve para esta
prueba: `purego-cef v0.13.3` exige Chromium/CEF 147 como mínimo. Con CEF 126,
el resultado esperado es una salida limpia con `unsupported CEF runtime`, no
un panic.

## Procedimiento de continuación

1. Instalar o montar un CEF 147+ compatible. El código busca `CEF_DIR`,
   `/usr/lib/cef` y `~/.local/share/cef`. Mantener los archivos auxiliares del
   paquete CEF junto a `libcef.so`, no solo la biblioteca.
2. Desde `v3/examples/cef-hello`, construir y ejecutar:

   ```bash
   go build -tags cef -o /tmp/cef-hello .
   CEF_DIR=/ruta/a/cef /tmp/cef-hello
   ```

3. Confirmar visualmente el HTML de ejemplo y revisar
   `/tmp/wails-cef-debug.log`. Deben aparecer las entradas posteriores a
   `cefCreateHostWindow`, incluida `cefCreateBrowserInWidget` y un navegador no
   nulo.
4. Probar el botón de IPC del ejemplo, eventos Go a JavaScript, resize,
   cierre y una segunda ventana. Documentar los resultados en
   `IMPLEMENTATION.md` antes de modificar la arquitectura de nuevo.
5. Si el navegador continúa sin crearse con CEF 147+, conservar el log y
   revisar primero la propiedad del contexto GLib en
   `linuxApp.isOnMainThread` y el callback
   `dispatchOnMainThreadCallback`; no volver a ejecutar GTK/CEF directamente
   desde la goroutine de `pending.Run()`.

## Validación requerida tras cambios

```bash
cd v3
go test -tags cef ./pkg/application ./pkg/doctor-ng/...
go test ./pkg/application
go test -tags gtk3 ./pkg/application

for tags in '' gtk3 server cef; do
  if [ -n "$tags" ]; then
    (cd examples/plain && go build -tags "$tags" -o "/tmp/wails-$tags" .)
  else
    (cd examples/plain && go build -o /tmp/wails-default .)
  fi
done
```

Los warnings de APIs X11 deprecadas de GTK4 son conocidos. La prueba server de
`pkg/application` falla por un problema ya existente de `go vet` en
`websocket_server.go`; no es una regresión de CEF.

## Alcance y decisiones vigentes

- Conservar `purego-cef`; no migrar a `energye/energy` sin una necesidad API
  comprobada. Energy es un framework propio con LCL y ciclo de aplicación,
  no un reemplazo directo del binding dentro de Wails.
- El backend CEF actual depende de X11 para adjuntar la vista CEF a la ventana
  GTK4. Verificar explícitamente la sesión de escritorio usada durante el
  smoke test.
- Añadir un workflow de CI para `-tags cef` después de disponer de un runtime
  CEF reproducible para el runner.

## Estado de herramientas y publicación

- `bd` y `coderabbit` no están instalados en este entorno.
- El remoto `origin` apunta a `https://github.com/wailsapp/wails.git`, pero
  `git push -u origin feat/linux-cef` falló por falta de credenciales.
- Antes de publicar: confirmar la base remota, ejecutar el análisis exigido
  por el repositorio, actualizar `IMPLEMENTATION.md`, commitear, rebasear y
  empujar la rama.
