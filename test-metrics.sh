#!/bin/bash

# Clean up any previous database
rm -f txn.db

# Start participant 1 (accounts-svc) on port 8081
PORT=:8081 ./participant &
PARTICIPANT1_PID=$!
echo "Participant 1 started with PID $PARTICIPANT1_PID on :8081"

# Start participant 2 (inventory-svc) on port 8082
PORT=:8082 ./participant &
PARTICIPANT2_PID=$!
echo "Participant 2 started with PID $PARTICIPANT2_PID on :8082"

# Wait for participants to start
sleep 2

# Start the coordinator in background
PORT=:8080 ./coordinator &
COORDINATOR_PID=$!
echo "Coordinator started with PID $COORDINATOR_PID"

# Wait for coordinator to start
sleep 2

echo "Testing metrics endpoint..."
curl -s http://localhost:8080/metrics | head -30

echo
echo "Creating a commit transaction to generate metrics..."
curl -s -X POST http://localhost:8080/transactions \
  -H "Content-Type: application/json" \
  -d '{
    "topic_key": "order-metrics-test",
    "operations": [
      {
        "participant": "inventory-svc",
        "action": "reserve",
        "params": {"item": "widget", "quantity": 1}
      }
    ],
    "timeout_ms": 5000
  }' > /dev/null

# Wait for transaction to complete
sleep 3

echo
echo "Metrics after transaction:"
curl -s http://localhost:8080/metrics | grep -E "^_pc_|^2pc_"

# Cleanup
echo
echo "Stopping services..."
kill $COORDINATOR_PID $PARTICIPANT1_PID $PARTICIPANT2_PID 2>/dev/null
wait $COORDINATOR_PID $PARTICIPANT1_PID $PARTICIPANT2_PID 2>/dev/null

echo "Metrics test complete!"
