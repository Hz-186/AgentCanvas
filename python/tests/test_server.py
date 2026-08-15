import json
import unittest

import grpc

from agentcanvas.pythonbridge.v1 import bridge_pb2, bridge_pb2_grpc
from agentcanvas_bridge.server import BridgeLimits, create_server


class ServerTest(unittest.TestCase):
    def setUp(self):
        self.server = create_server(
            "test-token",
            BridgeLimits(max_input_bytes=4096, max_output_bytes=4096, max_concurrency=2),
        )
        self.port = self.server.add_insecure_port("127.0.0.1:0")
        self.server.start()
        self.channel = grpc.insecure_channel(f"127.0.0.1:{self.port}")
        grpc.channel_ready_future(self.channel).result(timeout=3)
        self.stub = bridge_pb2_grpc.PythonBridgeStub(self.channel)

    @staticmethod
    def metadata(request_id="test-request"):
        return (
            ("x-agentcanvas-bridge-token", "test-token"),
            ("x-agentcanvas-request-id", request_id),
            ("x-agentcanvas-trace-id", "test-trace"),
        )

    def tearDown(self):
        self.channel.close()
        self.server.stop(0).wait()

    def test_health_is_available_without_auth_and_capabilities_require_auth(self):
        health = self.stub.Health(bridge_pb2.HealthRequest())
        self.assertEqual(health.status, "ok")
        with self.assertRaises(grpc.RpcError) as raised:
            self.stub.GetCapabilities(bridge_pb2.CapabilitiesRequest())
        self.assertEqual(raised.exception.code(), grpc.StatusCode.UNAUTHENTICATED)

        with self.assertRaises(grpc.RpcError) as missing_ids:
            self.stub.GetCapabilities(
                bridge_pb2.CapabilitiesRequest(), metadata=(("x-agentcanvas-bridge-token", "test-token"),)
            )
        self.assertEqual(missing_ids.exception.code(), grpc.StatusCode.INVALID_ARGUMENT)

        capabilities = self.stub.GetCapabilities(
            bridge_pb2.CapabilitiesRequest(), metadata=self.metadata()
        )
        self.assertEqual(capabilities.protocol_version, "v1")
        self.assertIn("python:recursive", capabilities.chunk_methods)
        tools = self.stub.ListTools(bridge_pb2.ListToolsRequest(), metadata=self.metadata("list-tools"))
        self.assertEqual(len(tools.tools), 2)

    def test_chunk_and_tool_execution(self):
        request = bridge_pb2.ChunkDocumentRequest(
            request_id="chunk-request",
            method="python:recursive",
            document=bridge_pb2.ParsedDocument(
                text="ignored",
                blocks=(
                    bridge_pb2.DocumentBlock(
                        id="heading",
                        type="heading",
                        text="# Guide",
                        metadata_json='{"title":"Guide"}',
                    ),
                    bridge_pb2.DocumentBlock(
                        id="body",
                        type="paragraph",
                        text="hello world",
                        bbox_json='{"x":1,"y":2,"width":3,"height":4}',
                        metadata_json='{"source":"test"}',
                    ),
                ),
            ),
            policy=bridge_pb2.ChunkPolicy(chunk_size=8, overlap=0),
        )
        metadata = self.metadata("chunk-request")
        response = self.stub.ChunkDocument(request, metadata=metadata)
        self.assertTrue(response.chunks)
        self.assertEqual(response.chunks[0].section_title, "Guide")
        self.assertEqual(json.loads(response.chunks[0].metadata_json)["bbox"]["x"], 1)

        tool_response = self.stub.ExecuteTool(
            bridge_pb2.ExecuteToolRequest(
                request_id="tool-request",
                tool_name="python_text_stats",
                arguments_json='{"text":"hello"}',
            ),
            metadata=self.metadata("tool-request"),
        )
        self.assertEqual(
            tool_response.content_json,
            '{"chars":5,"lines":1,"words":1,"sha256":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}',
        )

    def test_limits_and_unknown_tool_are_structured_errors(self):
        metadata = self.metadata()
        with self.assertRaises(grpc.RpcError) as oversized:
            self.stub.ChunkDocument(
                bridge_pb2.ChunkDocumentRequest(
                    request_id="test-request",
                    method="python:fixed_token",
                    document=bridge_pb2.ParsedDocument(text="x" * 5000),
                    policy=bridge_pb2.ChunkPolicy(chunk_size=8),
                ),
                metadata=metadata,
            )
        self.assertEqual(oversized.exception.code(), grpc.StatusCode.RESOURCE_EXHAUSTED)

        with self.assertRaises(grpc.RpcError) as missing:
            self.stub.ExecuteTool(
                bridge_pb2.ExecuteToolRequest(request_id="test-request", tool_name="python_missing", arguments_json="{}"),
                metadata=metadata,
            )
        self.assertEqual(missing.exception.code(), grpc.StatusCode.NOT_FOUND)

        with self.assertRaises(grpc.RpcError) as invalid:
            self.stub.ExecuteTool(
                bridge_pb2.ExecuteToolRequest(request_id="test-request", tool_name="python_json_transform", arguments_json="{}"),
                metadata=metadata,
            )
        self.assertEqual(invalid.exception.code(), grpc.StatusCode.INVALID_ARGUMENT)


if __name__ == "__main__":
    unittest.main()
