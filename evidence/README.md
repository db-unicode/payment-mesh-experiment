# Evidence directory

`raw/` y las corridas completas de `runs/` son generadas por el runner y se ignoran para evitar versionar HTML, logs y series temporales voluminosas. La corrida final puede conservar un subconjunto forzado en Git con los artefactos exactos que consume `mise run report`: metadatos, veredictos, CSV agregados, trazas, contadores y eventos de seguridad/fault injection.

La instantánea compacta versionada es `runs/20260911T202224Z/`. Para generar una nueva, ejecuta `mise run experiment-all` y luego `mise run report`; nunca sustituyas un `FAIL` por una afirmación manual.
