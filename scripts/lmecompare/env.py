# Local-only cognee config: Ollama LLM + Ollama embeddings, storage under
# BENCH_DIR (default: the current directory). Imported before cognee.
import os

WORK = os.path.abspath(os.environ.get("BENCH_DIR", os.getcwd()))
LLM = os.environ.get("BENCH_LLM", "llama3.1:8b")
OLLAMA = os.environ.get("OLLAMA_HOST_URL", "http://localhost:11434")

os.environ.update({
    "LLM_PROVIDER": "ollama",
    "LLM_MODEL": f"ollama/{LLM}",
    "LLM_ENDPOINT": OLLAMA,
    "LLM_API_KEY": "ollama",  # dummy, Ollama ignores it
    "EMBEDDING_PROVIDER": "ollama",
    "EMBEDDING_MODEL": "nomic-embed-text:latest",
    "EMBEDDING_ENDPOINT": f"{OLLAMA}/api/embed",
    "EMBEDDING_DIMENSIONS": "768",
    "HUGGINGFACE_TOKENIZER": "nomic-ai/nomic-embed-text-v1.5",
    "TELEMETRY_DISABLED": "1",
    "LLM_MAX_COMPLETION_TOKENS": "4096",
    "ENABLE_BACKEND_ACCESS_CONTROL": "false",
    "CACHING": "false",
    "SYSTEM_ROOT_DIRECTORY": os.path.join(WORK, "cognee_system"),
    "DATA_ROOT_DIRECTORY": os.path.join(WORK, "cognee_data"),
})
