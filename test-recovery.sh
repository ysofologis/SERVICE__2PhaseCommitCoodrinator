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

# Create a transaction that will be left in PREPARING state (timeout)
echo "Creating a transaction that will timeout..."
curl -s -X POST http://localhost:8080/transactions \
  -H "Content-Type: application/json" \
  -d '{
    "topic_key": "order-timeout",
    "operations": [
      {
        "participant": "accounts-svc",
        "action": "authorize",
        "params": {"account": "A", "amount": 5000}
      }
    ],
    "timeout_ms": 1000
  }' > /tmp/tx-timeout.json

TX_ID=$(grep -o '"id":"[^"]*"' /tmp/tx-timeout.json | head -1 | cut -d'"' -f4)
echo "Transaction ID: $TX_ID"

# Wait a bit for it to start preparing
sleep 1

# Kill the coordinator abruptly (simulate crash)
echo "Killing coordinator to simulate crash..."
kill -9 $COORDINATOR_PID 2>/dev/null
wait $COORDINATOR_PID 2>/dev/null

# Start coordinator again (should recover)
echo "Restarting coordinator (recovery mode)..."
PORT=:8080 ./coordinator &
COORDINATOR_PID_NEW=$!
echo "New coordinator PID: $COORDINATOR_PID_NEW"

# Wait for recovery to complete
sleep 3

# Check the transaction status after recovery
echo "Transaction status after recovery:"
curl -s http://localhost:8080/transactions/$TX_ID
echo

# List all transactions
echo "Listing all transactions:"
curl -s http://localhost:8080/transactions

# Cleanup
echo "Stopping services..."
kill $COORDINATOR_PID_NEW $PARTICIPANT1_PID $PARTICIPANT2_PID 2>/dev/null
wait $COORDINATOR_PID_NEW $PARTICIPANT1_PID $PARTICIPANT2_PID 2>/dev/null

echo "Recovery test complete!"
