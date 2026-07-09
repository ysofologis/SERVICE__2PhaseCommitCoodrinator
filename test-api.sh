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

# Test health endpoint
echo "Testing health endpoint..."
curl -s http://localhost:8080/healthz

# Test readyz endpoint
echo "Testing readyz endpoint..."
curl -s http://localhost:8080/readyz

# Create a transaction that will abort (insufficient funds: 15000 > 10000)
echo "Creating abort transaction..."
curl -s -X POST http://localhost:8080/transactions \
  -H "Content-Type: application/json" \
  -d '{
    "topic_key": "order-abort",
    "operations": [
      {
        "participant": "accounts-svc",
        "action": "authorize",
        "params": {"account": "A", "amount": 15000}
      },
      {
        "participant": "inventory-svc",
        "action": "reserve",
        "params": {"item": "widget", "quantity": 1}
      }
    ],
    "timeout_ms": 5000
  }' > /tmp/tx1.json

TX_ID1=$(grep -o '"id":"[^"]*"' /tmp/tx1.json | head -1 | cut -d'"' -f4)
echo "Transaction ID: $TX_ID1"

# Wait for it to complete (abort)
echo "Waiting for abort to complete..."
sleep 5

# Get final status
echo "Final status of abort transaction:"
curl -s http://localhost:8080/transactions/$TX_ID1
echo

# Create a transaction that will commit (single participant that always says yes)
echo "Creating commit transaction..."
curl -s -X POST http://localhost:8080/transactions \
  -H "Content-Type: application/json" \
  -d '{
    "topic_key": "order-commit",
    "operations": [
      {
        "participant": "inventory-svc",
        "action": "reserve",
        "params": {"item": "widget", "quantity": 1}
      }
    ],
    "timeout_ms": 5000
  }' > /tmp/tx2.json

TX_ID2=$(grep -o '"id":"[^"]*"' /tmp/tx2.json | head -1 | cut -d'"' -f4)
echo "Transaction ID: $TX_ID2"

# Wait for commit
sleep 3

echo "Final status of commit transaction:"
curl -s http://localhost:8080/transactions/$TX_ID2
echo

# List all transactions
echo "Listing all transactions:"
curl -s http://localhost:8080/transactions

# Cleanup
echo "Stopping services..."
kill $COORDINATOR_PID $PARTICIPANT1_PID $PARTICIPANT2_PID 2>/dev/null
wait $COORDINATOR_PID $PARTICIPANT1_PID $PARTICIPANT2_PID 2>/dev/null

echo "Test complete!"
