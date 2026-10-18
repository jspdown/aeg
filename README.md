# AEG

Agent Egress Gateway. 
AEG is a proxy that adds credentials to the HTTPS requests of an AI agent. 
The agent never have to hold a real secret.

```sh
aeg run --config aeg.yaml -- claude
```

AEG starts the command with a proxy and a CA that are set in its environment. 
It allows only the hosts and routes in the configuration, and it adds each credential only on the hosts that you name:

```yaml
version: 1
credentials:
  github:
    from:
      command: gh auth token
destinations:
  - host: api.github.com
    inject:
      - bearer: github
    routes:
      - methods: [GET]
        paths: ["/repos/acme/**"]
```

AEG is not a sandbox. 

To make sure that all traffic goes through AEG, run the agent in a sandbox that allows only the AEG port, 
for example [srt](https://github.com/anthropic-experimental/sandbox-runtime):

```sh
aeg run --config aeg.yaml -- srt --settings srt.json claude
```

## Security

AEG keeps the real credentials out of the agent, and sends each credential only to the hosts and routes in `aeg.yaml`.
It does not stop the agent from misuse of a credential on an allowed route.
Read the [threat model](docs/threat-model.md) for what AEG protects and what it does not.
To report a vulnerability, see [SECURITY.md](SECURITY.md).

## Build

Run the checks and build the binary to `result/bin/aeg`:

```sh
make
```
