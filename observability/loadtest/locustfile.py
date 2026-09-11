"""Six reproducible scenarios for the payment mesh experiment.

Select one with SCENARIO=happy|idempotency|decline|timeout|breaker|load.
The gateway mock is changed through its local admin endpoint by the runner.
"""
import json
import os
import threading
import uuid
from locust import HttpUser, between, task

SCENARIO = os.getenv("SCENARIO", "happy")
DEBTOR = os.getenv("DEBTOR_PARTICIPANT_ID", "participant-card")
CREDITOR = os.getenv("CREDITOR_PARTICIPANT_ID", "participant-bank")
INGRESS_HOST = os.getenv("INGRESS_HOST", "")

class PaymentUser(HttpUser):
    wait_time = between(0.05, 0.2)

    def on_start(self):
        self.key = str(uuid.uuid4())

    @task
    def payment(self):
        if SCENARIO == "idempotency":
            self.concurrent_same_key()
        else:
            key = str(uuid.uuid4())
            payload = {"amount_minor": 1200, "currency": "COP", "debtor_participant_id": DEBTOR, "creditor_participant_id": CREDITOR, "reference": key}
            headers = {"Idempotency-Key": key}
            if INGRESS_HOST:
                headers["Host"] = INGRESS_HOST
            with self.client.post("/v1/payments", json=payload, headers=headers, name=SCENARIO, catch_response=True) as response:
                expected = (202,) if SCENARIO in ("degraded", "timeout", "breaker") else ((422,) if SCENARIO == "decline" else (200,))
                if response.status_code not in expected:
                    response.failure(f"unexpected status {response.status_code}; expected {expected}")

    def concurrent_same_key(self):
        key = str(uuid.uuid4())
        payload = {"amount_minor": 1200, "currency": "COP", "debtor_participant_id": DEBTOR, "creditor_participant_id": CREDITOR, "reference": key}
        results = []
        def send():
            headers = {"Idempotency-Key": key}
            if INGRESS_HOST:
                headers["Host"] = INGRESS_HOST
            results.append(self.client.post("/v1/payments", json=payload, headers=headers, name="idempotency", catch_response=False).status_code)
        threads = [threading.Thread(target=send) for _ in range(10)]
        for thread in threads: thread.start()
        for thread in threads: thread.join()
        if sum(code in (200, 202) for code in results) < 1 or any(code not in (200, 202) for code in results):
            self.environment.events.request.fire(request_type="CHECK", name="idempotency-result", response_time=0, response_length=0, exception=Exception(json.dumps(results)))
