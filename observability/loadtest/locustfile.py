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
PARTICIPANT = os.getenv("PARTICIPANT_ID", "participant-card")

class PaymentUser(HttpUser):
    wait_time = between(0.05, 0.2)

    def on_start(self):
        self.key = str(uuid.uuid4())

    @task
    def payment(self):
        if SCENARIO == "idempotency":
            self.concurrent_same_key()
        else:
            payload = {"amount_minor": 1200, "currency": "COP", "instrument": "card", "participant_id": PARTICIPANT}
            with self.client.post("/v1/payments", json=payload, headers={"Idempotency-Key": self.key}, name=SCENARIO, catch_response=True) as response:
                if SCENARIO == "timeout" and response.status_code not in (200, 202):
                    response.failure(f"unexpected timeout status {response.status_code}")
                elif response.status_code not in (200, 202, 422):
                    response.failure(f"unexpected status {response.status_code}")

    def concurrent_same_key(self):
        payload = {"amount_minor": 1200, "currency": "COP", "instrument": "card", "participant_id": PARTICIPANT}
        results = []
        def send():
            results.append(self.client.post("/v1/payments", json=payload, headers={"Idempotency-Key": self.key}, name="idempotency", catch_response=False).status_code)
        threads = [threading.Thread(target=send) for _ in range(10)]
        for thread in threads: thread.start()
        for thread in threads: thread.join()
        if sum(code in (200, 202) for code in results) < 1 or any(code not in (200, 202) for code in results):
            self.environment.events.request.fire(request_type="CHECK", name="idempotency-result", response_time=0, response_length=0, exception=Exception(json.dumps(results)))
