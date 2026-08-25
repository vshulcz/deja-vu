# OpenAI embeddings

`deja embed` can use OpenAI's hosted embeddings endpoint in addition to local Ollama, LM Studio, and other OpenAI-compatible endpoints.

Set the OpenAI API key and point deja at the embeddings API:

```sh
export OPENAI_API_KEY='...'
export DEJA_EMBED_URL='https://api.openai.com/v1/embeddings'
export DEJA_EMBED_MODEL='text-embedding-3-small'
deja embed
```

When `DEJA_EMBED_URL` points to `api.openai.com`, deja uses `OPENAI_API_KEY` automatically.

For authenticated OpenAI-compatible endpoints, or to override the OpenAI key used for a request, set `DEJA_EMBED_KEY` explicitly:

```sh
export DEJA_EMBED_URL='https://example.com/v1/embeddings'
export DEJA_EMBED_MODEL='embedding-model'
export DEJA_EMBED_KEY='...'
deja embed
```

`DEJA_EMBED_KEY` takes precedence over `OPENAI_API_KEY`. `OPENAI_API_KEY` is only read for `api.openai.com`, so an OpenAI credential is not implicitly forwarded to local or third-party OpenAI-compatible endpoints.

Using a remote embedding endpoint sends the text being embedded to that provider. Local Ollama and LM Studio behavior is unchanged and requires no key.
