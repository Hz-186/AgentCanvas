"""Deterministic document chunking for the Bridge."""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from typing import Any, Iterable


@dataclass(slots=True)
class Block:
    block_id: str
    block_type: str
    text: str
    page_no: int | None = None
    metadata: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class Chunk:
    index: int
    content: str
    token_count: int
    char_count: int
    section_title: str = ""
    page_no: int | None = None
    metadata: dict[str, Any] = field(default_factory=dict)


def estimate_tokens(text: str) -> int:
    ascii_count = sum(ord(char) <= 127 for char in text)
    non_ascii_count = len(text) - ascii_count
    estimate = non_ascii_count + math.ceil(ascii_count / 4)
    return max(estimate, 1) if text else 0


def chunk_document(
    text: str,
    blocks: Iterable[Block],
    method: str,
    chunk_size: int,
    overlap: int,
) -> tuple[list[Chunk], str]:
    method = method.removeprefix("python:").strip() or "recursive"
    chunk_size = chunk_size if chunk_size > 0 else 800
    overlap = max(overlap, 0)
    if overlap >= chunk_size:
        overlap = chunk_size // 4
    block_list = list(blocks)
    if method == "fixed_token":
        return fixed_chunks(text, chunk_size, overlap), "estimated"
    if method == "recursive":
        return recursive_chunks(text, block_list, chunk_size, overlap), "estimated"
    if method == "langchain_recursive":
        return langchain_recursive_chunks(text, block_list, chunk_size, overlap), "estimated"
    raise ValueError(f"unsupported chunk method: {method}")


def langchain_recursive_chunks(
    text: str,
    blocks: list[Block],
    chunk_size: int,
    overlap: int,
) -> list[Chunk]:
    """Use LangChain's splitter while retaining AgentCanvas block metadata."""
    try:
        from langchain_core.documents import Document
        from langchain_text_splitters import RecursiveCharacterTextSplitter
    except ImportError as exc:  # pragma: no cover - deployment smoke test
        raise RuntimeError("LangChain text splitter dependencies are not installed") from exc

    segments = _document_segments(text, blocks)
    if not segments:
        return []
    splitter = RecursiveCharacterTextSplitter(
        chunk_size=chunk_size,
        chunk_overlap=overlap,
        length_function=estimate_tokens,
        separators=("\n\n", "\n", "。", "！", "？", "；", ";", "，", ",", " ", ""),
        keep_separator="end",
    )
    output: list[Chunk] = []
    grouped: list[Document] = []
    grouped_text: list[str] = []
    grouped_ids: list[str] = []
    grouped_metadata: dict[str, Any] = {}
    grouped_section = ""
    grouped_page: int | None = None

    def flush_group() -> None:
        nonlocal grouped_text, grouped_ids, grouped_metadata, grouped_section, grouped_page
        if not grouped_text:
            return
        grouped.append(
            Document(
                page_content="\n\n".join(grouped_text),
                metadata={
                    "block_ids": list(grouped_ids),
                    "section_title": grouped_section,
                    "page_no": grouped_page,
                    "source_metadata": dict(grouped_metadata),
                },
            )
        )
        grouped_text = []
        grouped_ids = []
        grouped_metadata = {}
        grouped_section = ""
        grouped_page = None

    for segment in segments:
        content = str(segment["text"]).strip()
        if not content:
            continue
        if segment["single"]:
            flush_group()
            atomic = Document(
                page_content=content,
                metadata={
                    "block_ids": [segment["id"]],
                    "section_title": segment["section"],
                    "page_no": segment["page"],
                    "source_metadata": dict(segment["metadata"]),
                },
            )
            atomic_parts = splitter.split_documents([atomic])
            for part in atomic_parts:
                output.append(_langchain_chunk(part, output))
            continue
        grouped_text.append(content)
        grouped_ids.append(str(segment["id"]))
        grouped_section = str(segment["section"] or grouped_section)
        if grouped_page is None:
            grouped_page = segment["page"]
        grouped_metadata = _merge_metadata(grouped_metadata, segment["metadata"])
    flush_group()

    for document in grouped:
        for part in splitter.split_documents([document]):
            output.append(_langchain_chunk(part, output))
    return output


def _langchain_chunk(document: Any, output: list[Chunk]) -> Chunk:
    metadata = dict(document.metadata or {})
    source_metadata = metadata.pop("source_metadata", {})
    block_ids = metadata.pop("block_ids", []) or []
    section_title = str(metadata.pop("section_title", "") or "")
    page_no = metadata.pop("page_no", None)
    if page_no is not None:
        page_no = int(page_no)
    merged = _metadata(
        "python:langchain_recursive",
        "estimated",
        [str(item) for item in block_ids],
        source_metadata,
        page_no,
    )
    merged["splitter"] = "langchain_recursive_character_v1"
    content = str(document.page_content).strip()
    return Chunk(
        index=len(output),
        content=content,
        token_count=estimate_tokens(content),
        char_count=len(content),
        section_title=section_title,
        page_no=page_no,
        metadata=merged,
    )


def fixed_chunks(text: str, chunk_size: int, overlap: int) -> list[Chunk]:
    runes = list(text.strip())
    if not runes:
        return []
    chunks: list[Chunk] = []
    start = 0
    while start < len(runes):
        end = _token_budget_end(runes, start, chunk_size)
        content = "".join(runes[start:end]).strip()
        if content:
            chunks.append(
                Chunk(
                    index=len(chunks),
                    content=content,
                    token_count=estimate_tokens(content),
                    char_count=len(content),
                    section_title=_infer_section_title(content),
                    metadata={"chunk_method": "python:fixed_token", "tokenizer": "estimated"},
                )
            )
        if end >= len(runes):
            break
        next_start = _token_overlap_start(runes, start, end, overlap)
        start = next_start if next_start > start else end
    return chunks


def recursive_chunks(
    text: str,
    blocks: list[Block],
    chunk_size: int,
    overlap: int,
) -> list[Chunk]:
    segments = _document_segments(text, blocks)
    if not segments:
        return []
    chunks: list[Chunk] = []
    buffer = _Buffer()

    def flush() -> None:
        nonlocal buffer
        content = buffer.content.strip()
        if not content:
            buffer = _Buffer()
            return
        chunks.append(
            Chunk(
                index=len(chunks),
                content=content,
                token_count=estimate_tokens(content),
                char_count=len(content),
                section_title=buffer.section_title,
                page_no=buffer.page_no,
                metadata=_metadata(
                    "python:recursive",
                    "estimated",
                    buffer.block_ids,
                    buffer.metadata,
                    buffer.page_no,
                ),
            )
        )
        buffer = _Buffer(
            content=_overlap_text(content, overlap),
            section_title=buffer.section_title,
            page_no=buffer.page_no,
        )

    for segment in segments:
        if segment["single"]:
            flush()
            content = segment["text"].strip()
            if content:
                chunks.append(
                    Chunk(
                        index=len(chunks),
                        content=content,
                        token_count=estimate_tokens(content),
                        char_count=len(content),
                        section_title=segment["section"],
                        page_no=segment["page"],
                        metadata=_metadata(
                            "python:recursive",
                            "estimated",
                            [segment["id"]],
                            segment["metadata"],
                            segment["page"],
                        ),
                    )
                )
            continue

        for piece in _split_recursive(segment["text"], chunk_size):
            piece = piece.strip()
            if not piece:
                continue
            candidate = piece if not buffer.content.strip() else f"{buffer.content.strip()}\n\n{piece}"
            if buffer.content and estimate_tokens(candidate) > chunk_size:
                flush()
                if buffer.content and estimate_tokens(f"{buffer.content}\n\n{piece}") > chunk_size:
                    buffer = _Buffer(section_title=buffer.section_title, page_no=buffer.page_no)
            if buffer.content:
                buffer.content += "\n\n"
            buffer.content += piece
            if segment["section"]:
                buffer.section_title = segment["section"]
            if buffer.page_no is None:
                buffer.page_no = segment["page"]
            if segment["id"]:
                buffer.block_ids.append(segment["id"])
            buffer.metadata = _merge_metadata(buffer.metadata, segment["metadata"])
    flush()
    for index, chunk in enumerate(chunks):
        chunk.index = index
    return chunks


@dataclass(slots=True)
class _Buffer:
    content: str = ""
    section_title: str = ""
    page_no: int | None = None
    block_ids: list[str] = field(default_factory=list)
    metadata: dict[str, Any] = field(default_factory=dict)

def _document_segments(text: str, blocks: list[Block]) -> list[dict[str, Any]]:
    if not blocks:
        text = text.strip()
        return [{"text": text, "section": _infer_section_title(text), "page": None, "id": "", "metadata": {}, "single": False}] if text else []
    segments: list[dict[str, Any]] = []
    section = ""
    for block in blocks:
        content = block.text.strip()
        if not content or block.block_type == "scrap":
            continue
        if block.block_type == "heading":
            section = content.lstrip("#").strip()
            continue
        single = block.metadata.get("chunk_hint") == "single_faq" or block.block_type in {"faq", "table", "list", "caption"}
        segments.append(
            {
                "text": content,
                "section": str(section or ""),
                "page": block.page_no,
                "id": block.block_id,
                "metadata": block.metadata,
                "single": single,
            }
        )
    return segments


def _split_recursive(text: str, budget: int, separators: tuple[str, ...] = ("\n\n", "\n", "。", "！", "？", ". ", "! ", "? ", "；", ";", "，", ",", " ")) -> list[str]:
    text = text.strip()
    if not text:
        return []
    if estimate_tokens(text) <= budget:
        return [text]
    if not separators:
        return _split_by_rune_budget(text, budget)
    separator = separators[0]
    parts = _split_keep_separator(text, separator)
    if len(parts) <= 1:
        return _split_recursive(text, budget, separators[1:])
    merged: list[str] = []
    buffer = ""
    for part in parts:
        part = part.strip()
        if not part:
            continue
        if estimate_tokens(part) > budget:
            if buffer:
                merged.append(buffer.strip())
                buffer = ""
            merged.extend(_split_recursive(part, budget, separators[1:]))
            continue
        candidate = f"{buffer} {part}".strip() if buffer else part
        if buffer and estimate_tokens(candidate) > budget:
            merged.append(buffer.strip())
            buffer = part
        else:
            buffer = candidate
    if buffer:
        merged.append(buffer.strip())
    return merged


def _split_keep_separator(text: str, separator: str) -> list[str]:
    if not separator:
        return [text]
    pieces = text.split(separator)
    return [piece + separator if index < len(pieces) - 1 and separator not in {"\n\n", "\n", " "} else piece for index, piece in enumerate(pieces)]


def _split_by_rune_budget(text: str, budget: int) -> list[str]:
    runes = list(text)
    output: list[str] = []
    start = 0
    while start < len(runes):
        end = _token_budget_end(runes, start, budget)
        output.append("".join(runes[start:end]).strip())
        start = end if end > start else start + 1
    return [part for part in output if part]


def _token_budget_end(runes: list[str], start: int, budget: int) -> int:
    end = start
    last_valid = start
    while end < len(runes):
        candidate = "".join(runes[start : end + 1]).strip()
        if not candidate:
            end += 1
            continue
        if estimate_tokens(candidate) > budget:
            break
        last_valid = end + 1
        end += 1
    return start + 1 if last_valid == start else last_valid


def _token_overlap_start(runes: list[str], start: int, end: int, overlap: int) -> int:
    if overlap <= 0:
        return end
    for index in range(end - 1, start - 1, -1):
        candidate = "".join(runes[index:end]).strip()
        if candidate and estimate_tokens(candidate) > overlap:
            return index + 1
    return start


def _overlap_text(content: str, overlap: int) -> str:
    if overlap <= 0:
        return ""
    runes = list(content)
    start = len(runes)
    while start > 0 and estimate_tokens("".join(runes[start - 1 :])) <= overlap:
        start -= 1
    return "".join(runes[start:]).strip() if start < len(runes) else ""


def _metadata(method: str, tokenizer: str, block_ids: list[str], extra: dict[str, Any], page_no: int | None) -> dict[str, Any]:
    metadata: dict[str, Any] = {"chunk_method": method, "tokenizer": tokenizer, "block_ids": list(block_ids)}
    metadata.update(extra)
    if page_no is not None:
        metadata["page_no"] = page_no
    return metadata


def _merge_metadata(current: dict[str, Any], next_metadata: dict[str, Any]) -> dict[str, Any]:
    merged = dict(current)
    for key, value in next_metadata.items():
        merged.setdefault(key, value)
    return merged


def _infer_section_title(content: str) -> str:
    for line in content.splitlines():
        line = line.strip()
        if line.startswith("#"):
            title = line.lstrip("#").strip()
            if title:
                return title
    return ""
