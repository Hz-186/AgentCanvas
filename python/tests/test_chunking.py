import unittest

from agentcanvas_bridge.chunking import Block, chunk_document, estimate_tokens


class ChunkingTest(unittest.TestCase):
    def test_recursive_chunking_preserves_heading_and_metadata(self):
        chunks, tokenizer = chunk_document(
            "ignored",
            [
                Block("h1", "heading", "# Guide", metadata={"title": "Guide"}),
                Block("b1", "paragraph", "alpha beta gamma", metadata={"source": "fixture"}),
                Block("b2", "paragraph", "delta epsilon zeta", metadata={"other": "value"}),
            ],
            "python:recursive",
            4,
            0,
        )
        self.assertEqual(tokenizer, "estimated")
        self.assertTrue(chunks)
        self.assertEqual(chunks[0].section_title, "Guide")
        self.assertEqual(chunks[0].metadata["chunk_method"], "python:recursive")
        self.assertIn("b1", chunks[0].metadata["block_ids"])

    def test_fixed_chunking_respects_budget(self):
        chunks, _ = chunk_document("# Intro\nabcdefghij", [], "python:fixed_token", 4, 0)
        self.assertTrue(chunks)
        self.assertTrue(all(chunk.token_count <= 4 for chunk in chunks))
        self.assertEqual(chunks[0].section_title, "Intro")
        self.assertEqual(estimate_tokens("中文"), 2)

    def test_recursive_chunking_preserves_single_block_page(self):
        chunks, _ = chunk_document(
            "ignored",
            [Block("faq1", "faq", "问题：什么是 Bridge？\n答案：独立 Python 服务。", page_no=7)],
            "python:recursive",
            4,
            0,
        )
        self.assertEqual(len(chunks), 1)
        self.assertEqual(chunks[0].page_no, 7)
        self.assertEqual(chunks[0].metadata["page_no"], 7)

    def test_empty_long_and_invalid_inputs(self):
        self.assertEqual(chunk_document("", [], "python:recursive", 8, 0)[0], [])
        chunks, _ = chunk_document("中文" * 1000, [], "python:recursive", 32, 4)
        self.assertTrue(chunks)
        self.assertTrue(all(chunk.token_count <= 32 for chunk in chunks))
        self.assertEqual(
            [chunk.content for chunk in chunks],
            [chunk.content for chunk in chunk_document("中文" * 1000, [], "python:recursive", 32, 4)[0]],
        )
        with self.assertRaises(ValueError):
            chunk_document("text", [], "python:unknown", 8, 0)


if __name__ == "__main__":
    unittest.main()
