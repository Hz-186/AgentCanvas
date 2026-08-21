"""LangChain-backed document parsing for the Python Bridge."""

from __future__ import annotations

import os
import tempfile
from pathlib import Path

from agentcanvas_bridge.chunking import Block

PARSER_METHOD = "python:langchain_pdf"
PARSER_VERSION = "langchain-pymupdf-v1"


def parse_document(content: bytes, filename: str, method: str) -> tuple[str, list[Block], bool, list[str]]:
    """Return normalized text blocks without leaking LangChain objects over gRPC."""
    normalized = method.strip() or PARSER_METHOD
    if normalized != PARSER_METHOD:
        raise ValueError(f"unsupported document parser: {method}")
    if Path(filename).suffix.lower() != ".pdf":
        raise ValueError(f"unsupported file type: {Path(filename).suffix.lstrip('.')}")
    if not content:
        return "", [], True, ["document is empty"]

    try:
        from langchain_community.document_loaders import PyMuPDFLoader
    except ImportError as exc:  # pragma: no cover - exercised by deployment smoke tests
        raise RuntimeError("LangChain PDF dependencies are not installed") from exc

    temp_path = ""
    try:
        with tempfile.NamedTemporaryFile(prefix="agentcanvas-", suffix=".pdf", delete=False) as handle:
            handle.write(content)
            temp_path = handle.name
        documents = PyMuPDFLoader(temp_path, mode="page").load()
    except Exception as exc:
        raise ValueError(f"PDF parsing failed: {exc}") from exc
    finally:
        if temp_path:
            try:
                os.unlink(temp_path)
            except OSError:
                pass

    total_pages = len(documents)
    blocks: list[Block] = []
    warnings: list[str] = []
    page_texts: list[str] = []
    for index, document in enumerate(documents, start=1):
        text = str(getattr(document, "page_content", "") or "").strip()
        if not text:
            warnings.append(f"page {index} has no extractable text")
            continue
        page_texts.append(text)
        blocks.append(
            Block(
                block_id=f"p{index}_b1",
                block_type="text",
                text=text,
                page_no=index,
                metadata={
                    "parser": "langchain_pymupdf",
                    "parser_version": PARSER_VERSION,
                    "file_type": "pdf",
                    "block_type": "text",
                    "page_no": index,
                    "total_pages": total_pages,
                },
            )
        )
    if not blocks:
        return "", [], True, warnings or ["PDF contains no extractable text"]
    if total_pages >= 3 and len(blocks) * 2 < total_pages:
        warnings.append("PDF appears scanned because most pages have no extractable text")
        return "", [], True, warnings
    return "\n\n".join(page_texts), blocks, False, warnings
