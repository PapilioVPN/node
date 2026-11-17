package settings_test

const validConfig = `startup_timeout: 5s
shutdown_timeout: 10s
communications:
  http:
    address: 127.0.0.1:0
    tls:
      certificate: certs/node.crt
      private_key: certs/node.key
      ca: certs/clients-ca.crt
    read_timeout: 1s
    write_timeout: 2s
    idle_timeout: 3s
    shutdown_timeout: 4s
    body_limit: 1024
    concurrency: 8
`
