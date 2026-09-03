# Configuration examples

Working `config.yaml` files for common setups. Copy one to the repository root
and adjust:

```bash
cp examples/traffic-limit.yaml config.yaml
```

| File                                                 | Purpose                                                                            |
|------------------------------------------------------|------------------------------------------------------------------------------------|
| [`minimal.yaml`](minimal.yaml)                       | Fill placeholders in the announce written in the panel, without replacing its text |
| [`announce-in-config.yaml`](announce-in-config.yaml) | Build the announce in the proxy instead of the panel                               |
| [`traffic-limit.yaml`](traffic-limit.yaml)           | Separate text for metered and unlimited plans                                      |
| [`user-status.yaml`](user-status.yaml)               | Warn users whose subscription is limited, expired or disabled                      |
| [`per-client.yaml`](per-client.yaml)                 | Target specific clients by user agent or client-type path                          |
| [`force-unlimited.yaml`](force-unlimited.yaml)       | Present every plan as unlimited while keeping the real quota internally            |
| [`localized-ru.yaml`](localized-ru.yaml)             | Russian text, local units and timezone                                             |

Every option is documented in [`config.example.yaml`](../config.example.yaml).

Two rules to keep in mind when combining these:

- **Rules are matched top to bottom, and the first match wins.** Put the most
  specific rule first; an unconditional rule makes every later rule for the same
  header unreachable, and the proxy refuses to start on such a config.
- **`template` replaces the header outright.** Omit it to keep the panel's text
  and only substitute the placeholders inside it.

These files are loaded by the test suite, so they are guaranteed to parse.
