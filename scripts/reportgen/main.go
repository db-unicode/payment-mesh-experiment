package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type metadata struct {
	Experiment, Hypothesis, Metric, Criterion, Commit, Timestamp string
	Users                                                        int    `json:"users"`
	SpawnRate                                                    int    `json:"spawn_rate"`
	Duration                                                     string `json:"duration"`
}
type verdict struct{ Status, Result, Evidence string }

func main() {
	runs, _ := os.ReadDir("evidence/runs")
	sort.Slice(runs, func(i, j int) bool { return runs[i].Name() > runs[j].Name() })
	var latest string
	if len(runs) > 0 {
		latest = filepath.Join("evidence/runs", runs[0].Name())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Informe del experimento Payment Mesh\n\nGenerado: `%s`\n\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("## Objetivo y método\n\nSe evalúan seis hipótesis sobre bounded contexts, zero-trust, resiliencia, normalización multi-proveedor, idempotencia y aislamiento de base de datos. El perfil predeterminado es 10 usuarios, incremento de 2 usuarios/s y 120 segundos; cada ejecución conserva metadatos, CSV/HTML de Locust, métricas, trazas, contadores, eventos y logs.\n\n")
	if latest == "" {
		b.WriteString("No hay ejecuciones en `evidence/runs/`; el resultado es `NOT EXECUTED`.\n")
	} else {
		fmt.Fprintf(&b, "Ejecución más reciente: `%s`.\n\n", latest)
	}
	b.WriteString("## Resultados por hipótesis\n\n| Experimento | Hipótesis | Métrica | Criterio | Resultado | Evidencia |\n|---|---|---|---|---|---|\n")
	if latest != "" {
		entries, _ := os.ReadDir(latest)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(latest, entry.Name())
			var m metadata
			var v verdict
			readJSON(filepath.Join(dir, "metadata.json"), &m)
			readJSON(filepath.Join(dir, "verdict.json"), &v)
			link := strings.TrimPrefix(dir+"/", "")
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | **%s**: %s | [%s](../%s) |\n", entry.Name(), m.Hypothesis, m.Metric, m.Criterion, v.Status, v.Result, link, link)
		}
	} else {
		b.WriteString("| seis experimentos | — | — | — | **NOT EXECUTED** | — |\n")
	}
	b.WriteString("\n## Interpretación y límites\n\nUn resultado `EXECUTED` significa que el runner completó sus comandos y conservó sus salidas; no convierte automáticamente una métrica en éxito: el criterio debe revisarse en los archivos de evidencia. `NOT_EXECUTED` identifica inyectores que requieren Kubernetes/Istio y no se inventan datos. El informe no incluye reconciliación asíncrona, colas ni workers.\n")
	if err := os.MkdirAll("docs", 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile("docs/report.md", []byte(b.String()), 0644); err != nil {
		panic(err)
	}
}
func readJSON(path string, value any) {
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, value)
	}
}
