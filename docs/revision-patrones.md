# Revisión de los patrones

La presencia de un patrón en el código y el cumplimiento de su criterio experimental son comprobaciones distintas. Las pruebas de Go verifican lógica y contratos; las métricas y trazas de una ejecución con Istio verifican el comportamiento del mesh.

| Patrón | Comprobación necesaria |
|---|---|
| Service Mesh | Sidecars inyectados, configuración aceptada y tráfico interno con mTLS STRICT. |
| Distributed Tracing | Una misma traza contiene operador, participantes, router y la pasarela externa; comprobar también la consulta de pagos. |
| Circuit Breaker | Reglas internas y externas; provocar fallos y comprobar expulsiones y reducción de solicitudes al proveedor. |
| Service Per Container | Construcción y despliegue independientes; detener una réplica del router durante carga. |
| Service Discovery | Destinos por nombre y recuperación con al menos 99 % de solicitudes exitosas en menos de 30 segundos. |
| Database Per Service | Credenciales y políticas separadas; denegar conexiones cruzadas y aislar la caída de participantes. |
| Idempotent Consumer | Diez duplicados exitosos producen una llamada y un cobro; contenido distinto con la misma clave devuelve conflicto; un pago interrumpido puede recuperarse con la misma clave. |
| Capa de Anticorrupción | Contratos distintos producen estados y errores internos estables sin mensajes específicos del proveedor. |

## Alcance de las evidencias

Un `PENDING` representa incertidumbre y no demuestra un pago exitoso. La presencia de un certificado activo no mide por sí sola el porcentaje de llamadas con mTLS. Una traza cualquiera tampoco demuestra todos los saltos del pago. Para la entrega deben conservarse los artefactos que sustentan cada criterio, además del resultado de las pruebas.

Los informes de corridas anteriores describen el código ejecutado en esas corridas; no certifican modificaciones posteriores.

## Verificación de esta revisión

Se ejecutaron `mise exec -- go test -race -count=1 ./...` y `mise run test-integration`, ambos con resultado satisfactorio. La integración crea PostgreSQL temporales y comprueba seis casos: diez duplicados con una llamada al proveedor; recuperación de un estado pendiente; compatibilidad del hash previo con moneda minúscula; fallo de persistencia posterior al cobro; conflicto de contenido con la misma clave; y reconciliación pública utilizando la solicitud interna almacenada.

Las pruebas de trazas incluyen recepción OTLP/HTTP real en un servidor de prueba, deserialización del mensaje protobuf y relación padre/hijo. Los manifiestos se renderizaron y analizaron estáticamente con Istio. Estas comprobaciones no sustituyen una nueva corrida de los seis experimentos de carga en Kubernetes; no se atribuyen a esta revisión los porcentajes ni latencias del informe histórico.

## Corrida posterior al reinicio

Se detuvieron las aplicaciones de Compose y Kubernetes, conservando los volúmenes, y se levantaron las imágenes corregidas en OrbStack. Los seis experimentos se ejecutaron en `evidence/runs/20260912T040738Z` con 10 usuarios, 2 usuarios/s y 120 segundos en cada escenario de carga. El resultado medido está en [el informe](report.md).

El despliegue real exigió corregir las comillas de `retryOn` en los mapas YAML; la validación del servidor de Kubernetes aceptó los manifiestos corregidos. La captura inicial de las trazas del flujo feliz precedió a la exportación de los spans del mock. Se conservó como `payment-operator-initial.json` y se recuperaron los mismos 20 trace IDs después de completarse la exportación: los 20 contienen todos los servicios.

La caída del router produjo un HTTP 502 entre 8.052 solicitudes; satisface el umbral del 99 %, aunque Locust devuelve código 1 por ese fallo individual. La recuperación observada quedó en el mismo segundo de la inyección; el registro usa resolución de segundos. No se obtuvo una serie útil de Prometheus para calcular el porcentaje de mTLS en pagos: el informe distingue esa limitación de la configuración de seguridad activa. Al terminar, los tres servicios y las tres bases de datos estaban listos, con dos réplicas de router y las dos pasarelas externas activas.
