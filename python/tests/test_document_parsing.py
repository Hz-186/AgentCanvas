import unittest

import pymupdf

from agentcanvas_bridge.document_parsing import PARSER_METHOD, parse_document


class DocumentParsingTest(unittest.TestCase):
    @staticmethod
    def pdf_bytes(*pages: str) -> bytes:
        document = pymupdf.open()
        for text in pages:
            page = document.new_page()
            if text:
                page.insert_text((72, 72), text)
        data = document.tobytes()
        document.close()
        return data

    def test_langchain_pdf_uses_one_based_pages_and_safe_metadata(self):
        text, blocks, requires_ocr, warnings = parse_document(
            self.pdf_bytes("first page", "second page"), "guide.pdf", PARSER_METHOD
        )
        self.assertFalse(requires_ocr)
        self.assertEqual(warnings, [])
        self.assertIn("first page", text)
        self.assertEqual([block.page_no for block in blocks], [1, 2])
        self.assertEqual(blocks[1].metadata["total_pages"], 2)
        self.assertEqual(blocks[0].metadata["file_type"], "pdf")
        self.assertNotIn("source", blocks[0].metadata)
        self.assertNotIn("file_path", blocks[0].metadata)

    def test_empty_pdf_requires_ocr(self):
        text, blocks, requires_ocr, warnings = parse_document(
            self.pdf_bytes(""), "scan.pdf", PARSER_METHOD
        )
        self.assertEqual(text, "")
        self.assertEqual(blocks, [])
        self.assertTrue(requires_ocr)
        self.assertTrue(warnings)

    def test_empty_unsupported_file_is_still_rejected(self):
        with self.assertRaises(ValueError):
            parse_document(b"", "note.txt", PARSER_METHOD)

    def test_malformed_pdf_is_rejected(self):
        with self.assertRaises(ValueError):
            parse_document(b"not a PDF", "broken.pdf", PARSER_METHOD)

    def test_blank_page_is_reported_without_shifting_page_numbers(self):
        text, blocks, requires_ocr, warnings = parse_document(
            self.pdf_bytes("", "visible text"), "mixed.pdf", PARSER_METHOD
        )
        self.assertFalse(requires_ocr)
        self.assertIn("visible text", text)
        self.assertEqual([block.page_no for block in blocks], [2])
        self.assertIn("page 1 has no extractable text", warnings)

    def test_mostly_blank_pdf_is_treated_as_probable_scan(self):
        text, blocks, requires_ocr, warnings = parse_document(
            self.pdf_bytes("", "", "visible header"), "mostly-scan.pdf", PARSER_METHOD
        )
        self.assertTrue(requires_ocr)
        self.assertEqual(text, "")
        self.assertEqual(blocks, [])
        self.assertTrue(any("appears scanned" in warning for warning in warnings))


if __name__ == "__main__":
    unittest.main()
