"""gRPC implementation for the AgentCanvas Python Bridge."""

from __future__ import annotations

import json
import hmac
import logging
import os
from concurrent import futures
from dataclasses import dataclass
from typing import Any

import grpc

from agentcanvas.pythonbridge.v1 import bridge_pb2, bridge_pb2_grpc
from agentcanvas_bridge.chunking import Block, chunk_document
from agentcanvas_bridge.document_parsing import PARSER_METHOD, PARSER_VERSION, parse_document
from agentcanvas_bridge.tools import TOOL_DEFINITIONS

logger = logging.getLogger(__name__)
PROTOCOL_VERSION = "v1"
SERVICE_VERSION = "0.1.0"


@dataclass(frozen=True, slots=True)
class BridgeLimits:
    max_input_bytes: int = 8 * 1024 * 1024
    max_output_bytes: int = 2 * 1024 * 1024
    max_concurrency: int = 8

    def __post_init__(self) -> None:
        if self.max_input_bytes <= 0 or self.max_output_bytes <= 0 or self.max_concurrency <= 0:
            raise ValueError("bridge limits must be positive")


def _metadata_json(value: dict[str, Any]) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def _parse_json_object(raw: str, field: str) -> dict[str, Any]:
    try:
        value = json.loads(raw or "{}")
    except json.JSONDecodeError as exc:
        raise ValueError(f"{field} must be valid JSON: {exc.msg}") from exc
    if value is None:
        return {}
    if not isinstance(value, dict):
        raise ValueError(f"{field} must be a JSON object")
    return value


class PythonBridge(bridge_pb2_grpc.PythonBridgeServicer):
    def __init__(self, token: str, limits: BridgeLimits | None = None) -> None:
        self._token = token.strip()
        self._limits = limits or BridgeLimits()

    def _require_auth(self, context: grpc.ServicerContext) -> str:
        if not self._token:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "bridge authentication token is not configured")
        metadata = dict(context.invocation_metadata())
        provided = metadata.get("x-agentcanvas-bridge-token", "")
        if not hmac.compare_digest(provided, self._token):
            context.abort(grpc.StatusCode.UNAUTHENTICATED, "invalid bridge authentication token")
        request_id = metadata.get("x-agentcanvas-request-id", "").strip()
        trace_id = metadata.get("x-agentcanvas-trace-id", "").strip()
        if not request_id or not trace_id:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "bridge request and trace IDs are required")
        return request_id

    def Health(self, request, context):  # noqa: N802
        return bridge_pb2.HealthResponse(
            status="ok",
            service_version=SERVICE_VERSION,
            protocol_version=PROTOCOL_VERSION,
            message="ready",
        )

    def GetCapabilities(self, request, context):  # noqa: N802
        self._require_auth(context)
        return bridge_pb2.CapabilitiesResponse(
            protocol_version=PROTOCOL_VERSION,
            service_version=SERVICE_VERSION,
            chunk_methods=["python:fixed_token", "python:recursive", "python:langchain_recursive"],
            parser_methods=[PARSER_METHOD],
            tools=[self._tool_capability(name, item) for name, item in TOOL_DEFINITIONS.items()],
            max_concurrency=self._limits.max_concurrency,
            max_input_bytes=self._limits.max_input_bytes,
            max_output_bytes=self._limits.max_output_bytes,
        )

    def ParseDocument(self, request, context):  # noqa: N802
        request_id = self._require_auth(context)
        if not request.request_id or request.request_id != request_id:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "request_id does not match bridge metadata")
        if not request.filename.strip() or not request.parser.strip():
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "filename and parser are required")
        if len(request.content) > self._limits.max_input_bytes:
            context.abort(grpc.StatusCode.RESOURCE_EXHAUSTED, "document exceeds bridge input limit")
        try:
            text, blocks, requires_ocr, warnings = parse_document(request.content, request.filename, request.parser)
            document = bridge_pb2.ParsedDocument(text=text, file_type="pdf")
            for block in blocks:
                item = document.blocks.add(id=block.block_id, type=block.block_type, text=block.text)
                if block.page_no is not None:
                    item.page_no = block.page_no
                item.metadata_json = _metadata_json(block.metadata)
            response = bridge_pb2.ParseDocumentResponse(
                document=document,
                parser=request.parser,
                implementation_version=PARSER_VERSION,
                requires_ocr=requires_ocr,
                warnings=warnings,
            )
            if len(response.SerializeToString()) > self._limits.max_output_bytes:
                context.abort(grpc.StatusCode.RESOURCE_EXHAUSTED, "parsed document exceeds bridge output limit")
            return response
        except ValueError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        except RuntimeError as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(exc))

    def ChunkDocument(self, request, context):  # noqa: N802
        request_id = self._require_auth(context)
        if not request.request_id or request.request_id != request_id:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "request_id does not match bridge metadata")
        if not request.method.strip() or not request.HasField("document") or not request.HasField("policy"):
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "method, document, and policy are required")
        if request.ByteSize() > self._limits.max_input_bytes:
            context.abort(grpc.StatusCode.RESOURCE_EXHAUSTED, "document exceeds bridge input limit")
        try:
            blocks = [
                self._block_from_proto(block) for block in request.document.blocks
            ]
            chunks, tokenizer = chunk_document(
                request.document.text,
                blocks,
                request.method,
                request.policy.chunk_size,
                request.policy.overlap,
            )
            response = bridge_pb2.ChunkDocumentResponse(
                algorithm=request.method,
                tokenizer=tokenizer,
                implementation_version=SERVICE_VERSION,
            )
            for chunk in chunks:
                item = response.chunks.add(
                    index=chunk.index,
                    content=chunk.content,
                    token_count=chunk.token_count,
                    char_count=chunk.char_count,
                    section_title=chunk.section_title,
                    metadata_json=_metadata_json(chunk.metadata),
                )
                if chunk.page_no is not None:
                    item.page_no = chunk.page_no
            if len(response.SerializeToString()) > self._limits.max_output_bytes:
                context.abort(grpc.StatusCode.RESOURCE_EXHAUSTED, "chunk response exceeds bridge output limit")
            return response
        except ValueError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        except RuntimeError as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(exc))

    def ListTools(self, request, context):  # noqa: N802
        self._require_auth(context)
        return bridge_pb2.ListToolsResponse(
            tools=[self._tool_capability(name, item) for name, item in TOOL_DEFINITIONS.items()]
        )

    def ExecuteTool(self, request, context):  # noqa: N802
        request_id = self._require_auth(context)
        if not request.request_id or request.request_id != request_id:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "request_id does not match bridge metadata")
        definition = TOOL_DEFINITIONS.get(request.tool_name)
        if definition is None:
            context.abort(grpc.StatusCode.NOT_FOUND, f"unknown Python tool: {request.tool_name}")
        if len(request.arguments_json.encode("utf-8")) > self._limits.max_input_bytes:
            context.abort(grpc.StatusCode.RESOURCE_EXHAUSTED, "tool arguments exceed bridge input limit")
        try:
            arguments = _parse_json_object(request.arguments_json, "arguments_json")
            value = definition["handler"](arguments)
            content_json = _metadata_json(value)
            if len(content_json.encode("utf-8")) > self._limits.max_output_bytes:
                context.abort(grpc.StatusCode.RESOURCE_EXHAUSTED, "tool output exceeds bridge output limit")
            return bridge_pb2.ExecuteToolResponse(
                content_json=content_json,
                content_text=content_json,
                is_error=False,
            )
        except ValueError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        except grpc.RpcError:
            raise
        except Exception as exc:  # pragma: no cover - defensive process boundary
            logger.exception("Python tool failed", extra={"tool": request.tool_name})
            context.abort(grpc.StatusCode.INTERNAL, "Python tool execution failed")

    @staticmethod
    def _tool_capability(name: str, definition: dict[str, Any]):
        return bridge_pb2.ToolCapability(
            name=name,
            description=definition["description"],
            parameters_json=_metadata_json(definition["parameters"]),
            risk_level=definition["risk_level"],
            side_effect=definition["side_effect"],
            version=definition["version"],
        )

    @staticmethod
    def _block_from_proto(block):
        metadata = _parse_json_object(block.metadata_json, "block.metadata_json")
        if block.bbox_json:
            try:
                metadata["bbox"] = json.loads(block.bbox_json)
            except json.JSONDecodeError as exc:
                raise ValueError(f"block.bbox_json must be valid JSON: {exc.msg}") from exc
        return Block(
            block_id=block.id,
            block_type=block.type,
            text=block.text,
            page_no=block.page_no if block.HasField("page_no") else None,
            metadata=metadata,
        )


def create_server(token: str, limits: BridgeLimits | None = None) -> grpc.Server:
    limits = limits or BridgeLimits()
    worker_count = limits.max_concurrency
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=worker_count),
        options=(
            ("grpc.max_receive_message_length", limits.max_input_bytes),
            ("grpc.max_send_message_length", limits.max_output_bytes),
        ),
    )
    bridge_pb2_grpc.add_PythonBridgeServicer_to_server(PythonBridge(token, limits), server)
    return server


def serve(host: str, port: int, token: str, limits: BridgeLimits | None = None) -> grpc.Server:
    server = create_server(token, limits)
    server.add_insecure_port(f"{host}:{port}")
    server.start()
    logger.info("Python Bridge listening", extra={"host": host, "port": port, "protocol": PROTOCOL_VERSION})
    return server


def config_from_env() -> tuple[str, int, str, BridgeLimits]:
    defaults = BridgeLimits()
    limits = BridgeLimits(
        max_input_bytes=int(os.getenv("AGENTCANVAS_PYTHON_BRIDGE_MAX_INPUT_BYTES", str(defaults.max_input_bytes))),
        max_output_bytes=int(os.getenv("AGENTCANVAS_PYTHON_BRIDGE_MAX_OUTPUT_BYTES", str(defaults.max_output_bytes))),
        max_concurrency=int(os.getenv("AGENTCANVAS_PYTHON_BRIDGE_MAX_CONCURRENCY", str(defaults.max_concurrency))),
    )
    return (
        os.getenv("AGENTCANVAS_PYTHON_BRIDGE_HOST", "127.0.0.1"),
        int(os.getenv("AGENTCANVAS_PYTHON_BRIDGE_PORT", "50051")),
        os.getenv("AGENTCANVAS_PYTHON_BRIDGE_TOKEN", ""),
        limits,
    )
