## Author
Shuwei Cui & Marco Huang 

## Distributed Group Membership Service

This project implements a **Distributed Group Membership Service** as part of the CS425 Distributed Systems course (Fall 2024). The service manages a group of machines in a distributed system, detecting when machines join, leave, or fail. It achieves time-bounded completeness with specific failure detection mechanisms.

## Overview

The project implements two failure detection mechanisms:

1. **PingAck**: A SWIM-style failure detection using a basic Ping-Ack protocol, without suspicion.
2. **PingAck+S**: An enhanced Ping-Ack protocol with a suspicion mechanism, which marks nodes as "suspect" before confirming failure.

Each machine is uniquely identified by a combination of its IP address, port, and timestamp.

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

## Features

- **Membership Management**: 
   - Joins, leaves, and failures are detected, and the membership list is updated accordingly across the group.
   - Each node is uniquely identified by its IP, port, and version number.

- **Failure Detection**:
   - Failures are detected using Ping-Ack or Ping-Ack+S protocols.
   - The suspicion mechanism allows for suspected failures before nodes are marked as failed.

- **Time-bounded Completeness**:
   - A failed node is detected and reflected in at least one membership list within 5 seconds, and in all membership lists within 10 seconds.

- **UDP-based Communication**:
   - Communication between machines is performed using UDP, ensuring low-overhead message passing.
   - The protocol ensures platform-independent communication.

- **Fault Tolerance**:
   - The system is robust against up to three simultaneous machine failures.
   - An introducer node is used to ensure proper initialization for new members.

