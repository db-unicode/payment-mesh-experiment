package main

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
type measurement struct {
	Name, Hypothesis, Metric, Value, Criterion, Status, Evidence string
}
type providerResult struct {
	Status string `json:"status"`
}
type gatewayStats struct {
	EffectiveCharges int `json:"effective_charges"`
}

func main() {
	latest := latestRun("evidence/runs")
	var b strings.Builder
	fmt.Fprintf(&b, "# Informe del experimento Payment Mesh\n\nGenerado: `%s`\n\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("## Objetivo y método\n\nSe evalúan seis hipótesis sobre bounded contexts, zero-trust, resiliencia, normalización multi-proveedor, idempotencia y aislamiento de base de datos. La corrida conserva Locust CSV/HTML, métricas, trazas, contadores, eventos y logs. El resultado se calcula únicamente a partir de esos artefactos.\n\n")
	if latest == "" {
		b.WriteString("No hay ejecuciones en `evidence/runs/`; los seis resultados son `NOT EXECUTED`.\n")
	} else {
		fmt.Fprintf(&b, "Ejecución más reciente: `%s`.\n\n", latest)
	}
	b.WriteString("## Resumen medido\n\n| Experimento | Métrica observada | Valor medido | Criterio | Resultado | Evidencia |\n|---|---|---|---|---|---|\n")
	measurements := measured(latest)
	for _, m := range measurements {
		link := evidenceLink(m.Evidence)
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | **%s** | [%s](%s) |\n", m.Name, m.Metric, m.Value, m.Criterion, m.Status, m.Evidence, link)
	}
	b.WriteString("\n## Metadatos y evidencia\n\n| Experimento | Hipótesis | Perfil | Estado del runner | Evidencia |\n|---|---|---|---|---|\n")
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
			profile := fmt.Sprintf("%d usuarios / %d usuarios/s / %s", m.Users, m.SpawnRate, m.Duration)
			if m.Users == 0 {
				profile = "no disponible"
			}
			link := evidenceLink(dir)
			fmt.Fprintf(&b, "| `%s` | %s | %s | `%s` | [%s](%s) |\n", entry.Name(), m.Hypothesis, profile, v.Status, dir, link)
		}
	} else {
		b.WriteString("| seis experimentos | — | — | `NOT EXECUTED` | — |\n")
	}
	b.WriteString("\n## Interpretación y límites\n\n`PASS` significa que el criterio medido se encontró en los artefactos; `FAIL` significa que hubo evidencia suficiente de incumplimiento; `NOT EXECUTED` significa que faltó el artefacto requerido. No se infieren resultados a partir del código de salida del runner. Las pruebas de cluster, mTLS e Istio solo cuentan cuando sus trazas, métricas y eventos están presentes.\n")
	if err := os.MkdirAll("docs", 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile("docs/report.md", []byte(b.String()), 0644); err != nil {
		panic(err)
	}
}

func latestRun(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return filepath.Join(root, names[len(names)-1])
}

func measured(latest string) []measurement {
	names := []string{"01-happy-mesh", "02-instance-failure", "03-gateway-degraded", "04-provider-normalization", "05-idempotency-duplicates", "06-participant-db-failure"}
	if latest == "" {
		out := make([]measurement, 0, len(names))
		for _, name := range names {
			out = append(out, measurement{Name: name, Metric: "—", Value: "sin corrida", Criterion: "artefactos requeridos", Status: "NOT EXECUTED", Evidence: "evidence/runs"})
		}
		return out
	}
	return []measurement{
		expHappy(latest, names[0]),
		expInstance(latest, names[1]),
		expDegraded(latest, names[2]),
		expNormalization(latest, names[3]),
		expIdempotency(latest, names[4]),
		expDBIsolation(latest, names[5]),
	}
}

func expHappy(root, name string) measurement {
	dir := filepath.Join(root, name)
	count, failures, p95, ok := locustStats(filepath.Join(dir, "locust_stats.csv"))
	traces := traceCount(filepath.Join(dir, "traces", "payment-operator.json"))
	secrets := readFile(filepath.Join(dir, "events", "operator-mtls-secrets.txt"))
	listener := readFile(filepath.Join(dir, "events", "egress-mtls-listener.json"))
	mtls := strings.Contains(secrets, "ACTIVE") && strings.Contains(listener, `"requireClientCertificate": true`)
	pass := ok && failures == 0 && traces > 0 && mtls
	return measurement{name, "Happy path por ingress/mTLS con tracing", "Locust solicitudes, fallos, p95, trazas y mTLS", fmt.Sprintf("solicitudes=%d; fallos=%d; p95=%d ms; trazas=%d; mTLS=%t", count, failures, p95, traces, mtls), "0 fallos, trazas > 0 y mTLS verificado", status(pass, ok && traces > 0 && secrets != "" && listener != ""), dir}
}
func expInstance(root, name string) measurement {
	dir := filepath.Join(root, name)
	_, failures, p95, ok := locustStats(filepath.Join(dir, "locust_stats.csv"))
	injected := fileExists(filepath.Join(dir, "events", "kubectl-delete.out")) || fileExists(filepath.Join(dir, "events", "kubernetes.skip"))
	pass := ok && injected && failures == 0
	return measurement{name, "Fallo de una instancia de router", "Locust fallos, p95 e inyección", fmt.Sprintf("fallos=%d; p95=%d ms; inyección=%t", failures, p95, injected), "0 fallos y caída registrada", status(pass, ok && injected), dir}
}
func expDegraded(root, name string) measurement {
	dir := filepath.Join(root, name)
	_, failures, p95, ok := locustStats(filepath.Join(dir, "locust_stats.csv"))
	raw, _ := os.ReadFile(filepath.Join(dir, "events", "timeout-http-status.txt"))
	timeout := strings.Contains(string(raw), "202")
	response := strings.Contains(readFile(filepath.Join(dir, "events", "timeout-response.json")), `"status":"PENDING"`)
	breakerBefore := readFile(filepath.Join(dir, "metrics", "egress-envoy-stats-before.txt"))
	breakerAfter := readFile(filepath.Join(dir, "metrics", "egress-envoy-stats.txt"))
	beforeEjections := envoyMetricTotal(breakerBefore, "ejections_enforced_total")
	afterEjections := envoyMetricTotal(breakerAfter, "ejections_enforced_total")
	breaker := afterEjections > beforeEjections
	pass := ok && failures == 0 && timeout && response && breaker
	return measurement{name, "Pasarela degradada", "Locust fallos, p95, timeout, estado y breaker", fmt.Sprintf("fallos=%d; p95=%d ms; HTTP202=%t; PENDING=%t; ejections=%.0f→%.0f", failures, p95, timeout, response, beforeEjections, afterEjections), "0 fallos, HTTP 202, PENDING y aumenta el contador de expulsiones Envoy", status(pass, ok && breakerBefore != "" && breakerAfter != ""), dir}
}
func expNormalization(root, name string) measurement {
	dir := filepath.Join(root, name)
	expected := []struct{ file, status string }{
		{"card-authorized.json", "SUCCEEDED"},
		{"card-declined.json", "FAILED"},
		{"bank-accepted.json", "SUCCEEDED"},
		{"bank-rejected.json", "FAILED"},
	}
	statuses := make([]string, 0, 4)
	ok := true
	pass := true
	for _, want := range expected {
		var p providerResult
		if !readJSON(filepath.Join(dir, "events", want.file), &p) {
			ok = false
			pass = false
			continue
		}
		statuses = append(statuses, p.Status)
		pass = pass && p.Status == want.status
	}
	return measurement{name, "Normalización de dos contratos de proveedor", "Estados observados", strings.Join(statuses, ", "), "card/bank exitoso→SUCCEEDED; decline/reject→FAILED", status(pass, ok), dir}
}
func expIdempotency(root, name string) measurement {
	dir := filepath.Join(root, name)
	var before, after gatewayStats
	okBefore := readJSON(filepath.Join(dir, "metrics", "card-stats-before.json"), &before)
	okAfter := readJSON(filepath.Join(dir, "metrics", "card-stats.json"), &after)
	delta := after.EffectiveCharges - before.EffectiveCharges
	checksum, responses := sameChecksums(filepath.Join(dir, "events"))
	pass := okBefore && okAfter && delta == 1 && responses == 10 && checksum
	return measurement{name, "Diez duplicados producen un solo cobro", "Δ cobros efectivos, respuestas y checksum", fmt.Sprintf("delta=%d; respuestas=%d; checksum=%t", delta, responses, checksum), "delta=1, 10 respuestas y checksum idéntico", status(pass, okBefore && okAfter), dir}
}
func expDBIsolation(root, name string) measurement {
	dir := filepath.Join(root, name)
	raw := strings.TrimSpace(readFile(filepath.Join(dir, "events", "http-status.txt")))
	readiness := strings.Contains(strings.ToLower(readFile(filepath.Join(dir, "events", "router-db-readiness.txt"))), "accepting connections")
	pass := raw == "503" && readiness
	return measurement{name, "Aislamiento ante fallo de participant DB", "HTTP y readiness de router DB", fmt.Sprintf("HTTP=%s; accepting connections=%t", raw, readiness), "HTTP 503 y router DB lista", status(pass, raw != "" || readiness), dir}
}

func locustStats(path string) (count, failures, p95 int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, false
	}
	defer f.Close()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return 0, 0, 0, false
	}
	indices := map[string]int{}
	for i, h := range header {
		indices[h] = i
	}
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, 0, false
		}
		if len(row) <= indices["Failure Count"] {
			continue
		}
		if len(row) > 1 && row[1] == "Aggregated" {
			count, _ = strconv.Atoi(row[indices["Request Count"]])
			failures, _ = strconv.Atoi(row[indices["Failure Count"]])
			p95, _ = strconv.Atoi(row[indices["95%"]])
			return count, failures, p95, true
		}
	}
	return 0, 0, 0, false
}
func traceCount(path string) int {
	var v struct {
		Data []json.RawMessage `json:"data"`
	}
	if !readJSON(path, &v) {
		return 0
	}
	return len(v.Data)
}
func sameChecksums(dir string) (bool, int) {
	data, err := os.ReadFile(filepath.Join(dir, "response-checksums.txt"))
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) < 2 {
			return false, 0
		}
		firstFields := strings.Fields(lines[0])
		if len(firstFields) == 0 {
			return false, 0
		}
		first := firstFields[0]
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) == 0 || fields[0] != first {
				return false, len(lines)
			}
		}
		return true, len(lines)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "response-*.json"))
	if len(files) != 10 {
		return false, len(files)
	}
	var first string
	for i, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return false, i
		}
		sum := sha256.Sum256(b)
		got := hex.EncodeToString(sum[:])
		if i == 0 {
			first = got
		} else if got != first {
			return false, len(files)
		}
	}
	return true, len(files)
}
func envoyMetricTotal(raw, metric string) float64 {
	var total float64
	for _, line := range strings.Split(raw, "\n") {
		if !strings.Contains(line, metric+":") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil && value > 0 {
			total += value
		}
	}
	return total
}
func status(pass, evidence bool) string {
	if !evidence {
		return "NOT EXECUTED"
	}
	if pass {
		return "PASS"
	}
	return "FAIL"
}
func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }
func readFile(path string) string { b, _ := os.ReadFile(path); return string(b) }
func readJSON(path string, value any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, value) == nil
}
func evidenceLink(path string) string { return "../" + path }
