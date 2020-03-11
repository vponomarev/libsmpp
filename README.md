LibSMPP
====================================
Library is developed to provide easy support of telecom protocol SMPP.

Bundle functionality:
----------------------------
libsmpp - library, that handles single SMPP session control, packet encode/decode and data exchange with your application via channels.
Also session pool support is presented, but now it's under deep development.

App smpp-dumb-server - SMPP Server emulator, can be used for function/load tests
App smpp-dumb-client - SMPP Client emulation, can be used for functional/load tests
App smpp-lb - Simple SMPP session load balancer

Current measured throughput.
AMD Ryzen 3600 running MS Windows 10 can handle up to ~30k SMS in a flow Server <=> Balancer <=> Client, where single SMS consists of: SMS (SUBMIT_SM + SUBMIT_SM_RESP packets) + Delivery Report (DELIVER_SM + DELIVER_SMP_RESP packets)

Implementation plans
----------------------------
Create easy to implement gateway from IT world interface to SMPP - possibly Kafka/RabbitMQ or Amazon SQS compatible protocol.

- Have any questions?
- Have suggestions?
- You're related to telecom operator and need some functionality?

Feel free to contact me.