## VM run command

- **Command Interface**:
```
   join         # Join the group.
   leave        # Voluntarily leave the group.
   list_mem     # List the current membership list.
   list_self    # Display the current node’s ID.
   enable_sus   # Enable suspicion mechanism.
   disable_sus  # Disable suspicion mechanism.
   status_sus   # Show whether suspicion mechanism is enabled or disabled.
```
- **VM Command Interface**:
```
    cd cs425_mp2_go/
    go build
    ./cs425_mp2_go
```

# Design Overview: Integration of Ping/Ack and Suspicion Mechanisms

## Introduction

This document provides a detailed explanation of the design and implementation of the membership service, focusing particularly on how the Ping/Ack mechanism is integrated with the suspicion mechanism to achieve efficient and reliable failure detection in a distributed system.

## Membership Protocol Overview

The membership service allows nodes in a distributed system to join and leave a group while maintaining an up-to-date list of active members. The protocol includes:

- **Joining and Leaving**: Nodes can join the group by contacting an introducer node and can leave gracefully by notifying others.
- **Failure Detection**: Nodes periodically check the liveness of other nodes using Ping messages.
- **Update Dissemination**: Changes in membership (joins, leaves, failures) are disseminated using gossip-style updates with a Time-To-Live (TTL).

## Ping/Ack Mechanism

### Ping Messages
- **Purpose**: Used to check the liveness of other nodes.
- **Process**:
  - A node selects a target node and sends a Ping message.
  - The Ping message may include piggybacked updates from the TTL cache.

### Ack Messages
- **Purpose**: Sent in response to a Ping to confirm that the node is alive.
- **Process**:
  - Upon receiving a Ping, the target node replies with an Ack message.
  - The Ack message may also include piggybacked updates.
  - If the target node is not in the sender’s membership list, it sets the reserved field in the header to `0xff`, prompting the sender to disseminate a join update.

## Suspicion Mechanism

### Purpose
The suspicion mechanism adds robustness to failure detection by introducing a suspect state before declaring a node as failed. This reduces false positives due to transient network issues.

### States
- **Alive**: The node is considered active and functioning correctly.
- **Suspect**: The node has missed a Ping/Ack exchange and is suspected of failing.
- **Failed**: The node is declared failed after a suspicion timeout expires.

### Process
1. **Missed Ack**: If a node does not receive an Ack in response to a Ping within a timeout period, it suspects the target node.
2. **Suspect Update**: The node creates a suspect update and disseminates it using the TTL cache.
3. **Suspicion Timeout**: A timer starts for the suspect node. If the node remains unresponsive until the timer expires, it is declared failed.
4. **Resume Update**: If the suspect node responds before the timeout, a resume update is created and disseminated.

## Integration of Ping/Ack and Suspicion Mechanisms

The integration of the Ping/Ack and suspicion mechanisms ensures efficient and accurate failure detection with minimal false positives.

### Ping Timeouts and Suspicion
- When a node sends a Ping, it starts a **PingAckTimeout** timer.
- If an Ack is not received before the timer expires:
  - **Suspicion Mechanism Disabled**: The node is immediately marked as failed.
  - **Suspicion Mechanism Enabled**:
    - The node is marked as suspect.
    - A suspect update is created and disseminated.
    - A **FailureTimeout** timer starts for the suspect node.
    - If the suspect node responds (e.g., via an Ack), a resume update is created.
    - If the FailureTimeout expires without a response, the node is marked as failed.

## Handling Updates

- **Receiving Suspect Updates**: Nodes receiving a suspect update for a member mark it as suspect and start their own FailureTimeout timers for the suspect node.
- **Receiving Resume Updates**: Nodes receiving a resume update for a member mark it as alive and cancel any existing FailureTimeout timers for that node.
- **Receiving Leave Updates**: Nodes remove the leaving node from their membership list.
- **Duplicate Update Detection**: Duplicate updates are tracked using the **DuplicateUpdateCaches** map to prevent redundant processing.

## Piggybacking Updates

- Both Ping and Ack messages can carry updates from the TTL cache.
- This allows for efficient dissemination of membership changes without additional messages.

## Flag Updates

- Nodes can enable or disable the suspicion mechanism.
- A **FlagUpdate** message is broadcast to inform other nodes of the change.
- Nodes update their suspicion mechanism status upon receiving a FlagUpdate.

## Edge Cases and Special Handling

- **Self-Suspicion**: If a node receives a suspect update about itself, it creates and disseminates a resume update.
- **Unknown Members**: If a node receives a Ping from an unknown node, it sets the reserved field in the Ack header to `0xff`, prompting the sender to disseminate a join update.
- **Introducer Failures**: Nodes periodically Ping the introducer. If the introducer is suspected or failed, nodes attempt to reconnect and update the membership list accordingly.

## Conclusion

The design effectively integrates the Ping/Ack mechanism with the suspicion mechanism to enhance failure detection in the distributed system. By utilizing timers and state transitions, the system balances responsiveness with robustness, minimizing false positives due to transient network issues. Piggybacking updates on Ping/Ack messages reduces network overhead, and the use of TTL caches ensures efficient dissemination of membership changes.