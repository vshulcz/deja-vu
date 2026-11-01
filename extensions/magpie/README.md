# Magpie Gateway Hook for Deja-vu

Brings historical session context from `deja` into Magpie API gateway requests automatically.

## Installation

```bash
magpie plugin add github:vshulcz/deja-vu
```

## How It Works

This plugin hooks into Magpie's asynchronous gateway requests (`onRequest`). When a request comes in from local or authenticated origins with a prompt of 4 or more characters, it queries `deja context` for relevant historical sessions and injects them as an untrusted reference block into the latest user message.

See [deja-vu repository](https://github.com/vshulcz/deja-vu).
