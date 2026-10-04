# Jona Homelab Companion para Windows

Servicio Windows 11 x64 para consultar estado y apagar un PC desde Jona Homelab. Ejecutable Go autocontenido, sin .NET. El servicio funciona aunque no haya una sesión iniciada; la bandeja solo muestra el código de emparejado y el estado.

## Instalar

1. Extrae el ZIP completo fuera de `C:\Program Files\JonaHomelabCompanion`.
2. Abre PowerShell **como administrador** en la carpeta extraída:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1
```

El instalador conserva el emparejado, crea el servicio `JonaHomelabCompanion` como `LocalSystem` con arranque automático retrasado y recuperación ante errores. Abre TCP 47654 únicamente para la subred local en el perfil **Privado**. Comprueba la versión y la API antes de dar la instalación por terminada. No cambies el perfil de una red pública o no confiable para habilitarlo.

La bandeja se inicia diez segundos después de entrar en Windows, también con batería y sin límite de duración. Cada sesión interactiva tiene su propio icono. Si Explorer tarda en arrancar o se reinicia, Companion reintenta registrar el icono.

Si el servicio todavía no ha arrancado, la bandeja reintenta conectarse cada cinco segundos, incluso con la ventana oculta. Recupera el estado y el código de emparejado cuando el servicio responde, sin mostrar errores modales durante el arranque ni necesitar **Refresh**. Al abrir la ventana mientras espera, conserva la comprobación de actualizaciones hasta conectar.

Abrir `JonaHomelab.Companion.exe` con doble clic muestra la ventana. Si ya hay una instancia en esa sesión, activa su ventana y la restaura si está minimizada. `--tray` conserva el arranque silencioso; `--show` abre la ventana expresamente. El servicio sigue usando `--service`.

Para iniciar la bandeja manualmente:

```powershell
Start-ScheduledTask -TaskName JonaHomelabCompanionTray
```

Un fallo al crear la bandeja no detiene el servicio. Para omitirla expresamente: `install.ps1 -SkipTray`.

El instalador y las actualizaciones automáticas aplican estos ajustes a la tarea existente. Para reparar solo el arranque de una instalación actualizada, ejecuta `tray-task.ps1` como administrador desde `current` y después inicia la tarea con el comando anterior.

El icono de servidores identifica el Companion en la bandeja y reaparece si Explorer se reinicia. Tanto el clic izquierdo como el derecho abren la ventana o la restauran si está minimizada. El icono no tiene menú contextual; las acciones están disponibles en la ventana. Los avisos y confirmaciones usan el tema oscuro de la aplicación, admiten teclado y se adaptan al escalado de Windows. En la confirmación para rotar el código, **Cancel** recibe el foco inicial; Escape o cerrar el diálogo cancela la acción.

## Conectar con la web

1. Abre la bandeja y copia el código `jhcp1_...`.
2. En Jona Homelab, edita el dispositivo, selecciona **Companion**, introduce la IPv4 privada o nombre del PC y pega el código.
3. Guarda y actualiza el estado. Debe aparecer **Companion ready**.
4. Pulsa **Power options** y elige **Shut down**, **Sleep** o **Hibernate**. Confirma la acción. El apagado seguro permite que aplicaciones bloqueen el apagado; el forzado puede perder trabajo sin guardar.

Suspender e hibernar requieren este Companion actualizado y los estados habilitados en Windows. La API nativa comprueba suspensión clásica o Modern Standby y, para hibernar, un archivo de hibernación completo. No habilita ni cambia la configuración de energía automáticamente. `powercfg /a` muestra los estados disponibles; el ZIP de diagnóstico incluye esa información. SSH conserva solo el apagado.

Las nuevas acciones usan `POST /v1/power` con `{ "action": "sleep" | "hibernate" | "shutdown", "force": false }`, firmado con el mismo protocolo. `force: true` solo se permite para apagar. El endpoint anterior `/v1/shutdown` se conserva. Todas las acciones comparten diez segundos de espera y no se reintentan automáticamente.

Suspensión e hibernación se programan antes de cambiar el estado del equipo para que la web reciba respuesta. Las trazas `power.scheduled`, `power.executing`, `power.completed`, `power.failed` y `power.rejected` muestran el resultado. La aceptación no prueba que el cambio de estado haya terminado. Se conservan los eventos de reactivación de Windows; Wake-on-LAN depende del hardware y de su configuración.

La conexión sale del servidor de Jona Homelab. Ese servidor debe alcanzar el PC por TCP 47654 en la red privada. No hay que configurar SSH. Los relojes de ambos equipos deben estar sincronizados (tolerancia de 60 segundos).

El protocolo firma peticiones y respuestas con HMAC-SHA256, rechaza repeticiones y no devuelve la clave a la web. El secreto se guarda con DPAPI de máquina y ACL para administradores/SYSTEM. La bandeja accede mediante un canal local con límite de espera, inaccesible desde la red.

## Trazas y diagnóstico

Desde la carpeta extraída o `C:\Program Files\JonaHomelabCompanion\current`, ejecuta:

```powershell
.\diagnostics.ps1
```

Genera un **ZIP en la subcarpeta `diagnostics` junto a `diagnostics.ps1` y al ejecutable**, independientemente de la carpeta desde la que lo invoques. Puedes compartirlo para investigar errores. Funciona aunque el servicio esté parado. Ejecutarlo como administrador permite recoger todas las secciones y escribir en la instalación de `Program Files`.

También puedes pulsar **Generate diagnostics** en la ventana del Companion. Windows solicita permiso de administrador para esta operación. La ventana sigue respondiendo mientras se genera el informe y confirma la carpeta de destino al terminar. **Open folder** abre esa carpeta en el Explorador sin pedir elevación; **Close** cierra el aviso. Si cancelas el permiso o falla la generación, muestra el resultado sin ofrecer abrir la carpeta ni cambiar el estado de conexión del servicio.

Para otro destino desde PowerShell:

```powershell
.\diagnostics.ps1 -OutputDirectory "$env:USERPROFILE\Downloads"
```

Incluye estado del servicio, comprobación HTTP, red, regla de firewall, eventos recientes y trazas. También recoge configuración y último resultado de la tarea de bandeja, procesos de Companion/Explorer con sus sesiones y eventos del Programador de tareas si están disponibles. Contiene IP locales y rutas; **excluye el código de emparejado, las firmas y `config.json`**. No envía nada automáticamente. Una sección inaccesible queda registrada sin impedir las demás.

Archivos en `C:\ProgramData\JonaHomelabCompanion`:

- `service.log`: JSON por línea con fecha UTC, PID, arranque/parada, llamadas autenticadas, rechazos, resultado del apagado y actualizaciones. Rota a `service.log.1` al alcanzar 2 MiB.
- `install.log`: pasos y errores del instalador; conserva la ejecución anterior como `.1`.
- `crash.log`: errores fatales del proceso; conserva el fallo anterior como `.1`.

`shutdown.failed` incluye el error de Windows. `shutdown.accepted` significa que Windows aceptó la orden, no que el equipo ya esté apagado. Un fallo de ping tampoco demuestra apagado. Si no llega ninguna petición al log, revisa IP, perfil de red y firewall. `request.rejected` distingue firma/reloj, dirección no privada y nonce repetido.

## Paquetes locales y actualizaciones

La versión visible de Companion procede de `companion/VERSION` y empieza en `1.00`. Cada commit incrementa el contador (`1.01`, `1.02`, …, `1.100`). Se incorpora al binario al compilar y se muestra tanto en la ventana de Companion como en su ficha web. El identificador `main-<commit>` sigue identificando los paquetes para actualizar, verificar y restaurar instalaciones; las versiones antiguas que no informan del número visible muestran ese identificador.

Al abrir la ventana desde la bandeja se refresca la información y se comprueba la última release mediante el mismo sistema que **Check for updates**. Si encuentra una actualización, el sistema existente programa su instalación. La comprobación se ejecuta en segundo plano y muestra el resultado junto a la versión; las compilaciones locales conservan la instalación manual. Activar una ventana que ya está abierta no repite la consulta.

La web muestra la versión instalada y la última release que incluye el ZIP de Companion y su checksum. La consulta a GitHub se comparte entre dispositivos y se almacena durante cinco minutos; los errores se reintentan después de treinta segundos y no cambian el estado de conexión del PC.

En versiones compatibles, **Actualizar Companion** envía una petición firmada a `POST /v1/update` con un objeto vacío. Solo se admiten los archivos de la release publicada del repositorio configurado. La consulta normal `GET /v1/status` no instala nada: devuelve versión, capacidad `remoteUpdate` y estado de la operación. Las versiones antiguas siguen mostrando su versión y necesitan una primera instalación manual para incorporar esta capacidad.

La descarga, verificación, instalación, reinicio y resultado quedan en `update-status.json`, incluido en los diagnósticos. Un bloqueo exclusivo evita dos instaladores concurrentes, incluso durante el reinicio del servicio. El panel confirma la actualización únicamente cuando recibe una respuesta firmada con la versión objetivo. **Update requested** en la bandeja solo confirma la petición; **Refresh** muestra progreso y errores. Se reinicia Companion, no Windows, y se conserva el emparejamiento.

Si falla la comprobación de salud de la nueva versión, se detiene antes de restaurar la junction de la versión anterior. Los fallos previos a activar la nueva versión dejan el servicio anterior funcionando. Tras un fallo se evita un reintento automático inmediato; la opción manual permite reintentar.

Una instalación afectada por `update.finished` con `exitCode: 2` inmediatamente después de `update.starting` puede requerir instalar manualmente la release que contiene esta reparación. El actualizador anterior puede no resolver la junction `current` y, por tanto, no puede instalar su propia corrección. El nuevo actualizador resuelve la ruta mediante Windows y registra el paso concreto que falla.

Las versiones `local-...` permiten probar una reparación y no se actualizan automáticamente a una release anterior. En ellas, **Check for updates** muestra una explicación informativa y mantiene el servicio conectado; no intenta descargar ni instalar nada. Para habilitar actualizaciones automáticas hay que instalar un paquete publicado `main-...`, que mantiene la comprobación diaria, checksum y rollback. Un error del comprobador se registra y se muestra sin confundirlo con una caída del servicio; solo un fallo de conexión al servicio muestra **Service unavailable**.

Para generar un ZIP desde el código, con Go instalado, sin compilar la web:

```powershell
.\companion\package.ps1
```

El ZIP y su SHA256 quedan en `artifacts`. El binario no está firmado con Authenticode.

## Pruebas sin apagar el PC

La consola solo permite simulación, escucha únicamente en localhost y guarda sus datos en una carpeta aislada:

```powershell
.\JonaHomelab.Companion.exe --console --simulate-shutdown C:\Temp\companion-prueba
```

El código temporal queda en `C:\Temp\companion-prueba\JonaHomelabCompanion\pairing-code.txt`. `/health` indica `simulated: true`; las trazas usan `shutdown.simulated` o `power.simulated`. Este modo no instala servicios, no modifica firewall y nunca ejecuta apagado, suspensión ni hibernación reales. No lo uses como sustituto de la prueba del servicio instalado.

## Desinstalar

En PowerShell como administrador:

```powershell
.\uninstall.ps1
```

Conserva la configuración. Añade `-PurgeData` solo para borrar también el emparejado y las trazas.
