package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeReportFixture(t *testing.T, root, name, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, name, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func locustFixture() string {
	return "Type,Name,Request Count,Failure Count,95%,99%\n,Aggregated,100,0,20,30\n"
}

func TestExpInstanceSkippedNeverPasses(t *testing.T) {
	root := t.TempDir()
	writeReportFixture(t, root, "02-instance-failure", "events/kubernetes.skip", "cluster unavailable\n")
	writeReportFixture(t, root, "02-instance-failure", "locust_stats.csv", locustFixture())
	got := expInstance(root, "02-instance-failure")
	if got.Status != "NOT EXECUTED" {
		t.Fatalf("status = %q, want NOT EXECUTED", got.Status)
	}
}

func TestExpInstanceRequiresMeasuredRecovery(t *testing.T) {
	root := t.TempDir()
	name := "02-instance-failure"
	writeReportFixture(t, root, name, "locust_stats.csv", locustFixture())
	writeReportFixture(t, root, name, "events/locust.exit", "0\n")
	writeReportFixture(t, root, name, "events/kubectl-delete.out", "pod deleted\n")
	writeReportFixture(t, root, name, "events/fault-injection-start-epoch.txt", "100\n")
	writeReportFixture(t, root, name, "events/fault-injection-recovery-epoch.txt", "140\n")
	writeReportFixture(t, root, name, "events/fault-recovery-seconds.txt", "40\n")
	writeReportFixture(t, root, name, "events/router-recovery.out", "pod ready\n")
	writeReportFixture(t, root, name, "events/traffic-recovery-epoch.txt", "140\n")
	writeReportFixture(t, root, name, "events/traffic-recovery-seconds.txt", "40\n")
	got := expInstance(root, name)
	if got.Status != "FAIL" {
		t.Fatalf("status = %q, want FAIL for recovery >=30s", got.Status)
	}
}

func TestExpHappyRequiresGatewayTraceInSameTrace(t *testing.T) {
	root := t.TempDir()
	name := "01-happy-mesh"
	writeReportFixture(t, root, name, "locust_stats.csv", locustFixture())
	writeReportFixture(t, root, name, "events/operator-mtls-secrets.txt", "default ACTIVE true\n")
	writeReportFixture(t, root, name, "events/egress-mtls-listener.json", `{"requireClientCertificate": true}`)
	writeReportFixture(t, root, name, "traces/payment-operator.json", `{"data":[{"traceID":"one","spans":[{"traceID":"one","processID":"operator"},{"traceID":"one","processID":"router"}],"processes":{"operator":{"serviceName":"payment-operator.payments"},"router":{"serviceName":"payment-router.payments"}}}]}`)
	got := expHappy(root, name)
	if got.Status != "NOT EXECUTED" {
		t.Fatalf("status = %q, want NOT EXECUTED without gateway span", got.Status)
	}
}

func TestTraceCoverageRequiresOneCompleteTrace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "traces.json")
	data := `{"data":[{"traceID":"one","spans":[{"traceID":"one","processID":"operator"}],"processes":{"operator":{"serviceName":"payment-operator"}}},{"traceID":"two","spans":[{"traceID":"two","processID":"operator"},{"traceID":"two","processID":"participant"},{"traceID":"two","processID":"router"},{"traceID":"two","processID":"gateway"}],"processes":{"operator":{"serviceName":"payment-operator"},"participant":{"serviceName":"participant-payment-manager"},"router":{"serviceName":"payment-router"},"gateway":{"serviceName":"gateway-card"}}}]}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	total, complete := traceCoverage(path)
	if total != 2 || complete != 1 {
		t.Fatalf("traceCoverage = (%d, %d), want (2, 1)", total, complete)
	}
}

func writeMTLSFixture(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "istio-mtls.json")
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadMTLSRequiresBothInternalHopsAndMeasuresMutualTLS(t *testing.T) {
	path := writeMTLSFixture(t, "{\"status\":\"success\",\"data\":{\"resultType\":\"vector\",\"result\":[{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"participant-payment-manager\",\"connection_security_policy\":\"mutual_tls\"},\"value\":[1,\"4\"]},{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"payment-router\",\"connection_security_policy\":\"mutual_tls\"},\"value\":[1,\"6\"]}]}}")
	got := readMTLS(path)
	if !got.Valid || got.Percent != 100 || got.NonTLS != 0 || got.Unknown != 0 {
		t.Fatalf("readMTLS = %#v, want valid 100%% mutual TLS", got)
	}
}

func TestReadMTLSRejectsNonTLSTraffic(t *testing.T) {
	path := writeMTLSFixture(t, "{\"status\":\"success\",\"data\":{\"resultType\":\"vector\",\"result\":[{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"participant-payment-manager\",\"connection_security_policy\":\"mutual_tls\"},\"value\":[1,\"4\"]},{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"payment-router\",\"connection_security_policy\":\"none\"},\"value\":[1,\"6\"]}]}}")
	got := readMTLS(path)
	if !got.Valid || got.Percent != 40 || got.NonTLS != 6 {
		t.Fatalf("readMTLS = %#v, want valid 40%% with six non-mTLS requests", got)
	}
}

func TestReadMTLSCountsUnknownPolicySeparately(t *testing.T) {
	path := writeMTLSFixture(t, "{\"status\":\"success\",\"data\":{\"resultType\":\"vector\",\"result\":[{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"participant-payment-manager\",\"connection_security_policy\":\"unknown\"},\"value\":[1,\"4\"]},{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"payment-router\",\"connection_security_policy\":\"none\"},\"value\":[1,\"6\"]}]}}")
	got := readMTLS(path)
	if !got.Valid || got.Unknown != 4 || got.NonTLS != 6 {
		t.Fatalf("readMTLS = %#v, want valid measurement with four unknown and six non-mTLS", got)
	}
}

func TestReadMTLSDoesNotInferSuccessFromEmptyOrMissingHop(t *testing.T) {
	empty := readMTLS(writeMTLSFixture(t, "{\"status\":\"success\",\"data\":{\"resultType\":\"vector\",\"result\":[]}}"))
	if empty.Valid || empty.Percent != 0 {
		t.Fatalf("empty mTLS result = %#v, want invalid zero measurement", empty)
	}
	missingHop := readMTLS(writeMTLSFixture(t, "{\"status\":\"success\",\"data\":{\"resultType\":\"vector\",\"result\":[{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"payment-router\",\"connection_security_policy\":\"mutual_tls\"},\"value\":[1,\"5\"]}]}}"))
	if missingHop.Valid || missingHop.Total != 5 {
		t.Fatalf("missing mTLS hop = %#v, want invalid measurement", missingHop)
	}
	zeroHop := readMTLS(writeMTLSFixture(t, "{\"status\":\"success\",\"data\":{\"resultType\":\"vector\",\"result\":[{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"participant-payment-manager\",\"connection_security_policy\":\"mutual_tls\"},\"value\":[1,\"0\"]},{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"payment-router\",\"connection_security_policy\":\"mutual_tls\"},\"value\":[1,\"5\"]}]}}"))
	if zeroHop.Valid || zeroHop.Hops["participant-payment-manager"] {
		t.Fatalf("zero-valued hop = %#v, want invalid without participant traffic", zeroHop)
	}
	malformed := readMTLS(writeMTLSFixture(t, "{\"status\":\"success\",\"data\":{\"resultType\":\"vector\",\"result\":[{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"participant-payment-manager\",\"connection_security_policy\":\"mutual_tls\"},\"value\":[1,\"NaN\"]},{\"metric\":{\"source_workload\":\"payment-operator\",\"destination_workload\":\"payment-router\",\"connection_security_policy\":\"mutual_tls\"},\"value\":[1,\"5\"]}]}}"))
	if malformed.Valid {
		t.Fatalf("malformed mTLS series = %#v, want invalid", malformed)
	}
}
