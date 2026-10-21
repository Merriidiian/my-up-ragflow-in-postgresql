import tempfile
import unittest
from pathlib import Path
from check_ocr_vocabulary import inspect_vocabulary


class VocabularyContractTests(unittest.TestCase):
    def check(self, vocabulary, text):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'ocr.res'
            path.write_text(vocabulary, encoding='utf-8')
            return inspect_vocabulary(path, text)

    def test_latin_control_does_not_certify_russian(self):
        result = self.check('F\nO\nR\nM\nA\nT\n-\n5\n8\n3\n2\n1\n', 'Нева-31')
        self.assertEqual(result['missing_characters'], ['Н', 'а', 'в', 'е'])
        self.assertFalse(result['representable'])

    def test_unicode_alphabet_and_space(self):
        result = self.check('Н\nе\nв\nа\n-\n3\n1\n', 'Нева-31 Нева')
        self.assertTrue(result['representable'])
        self.assertEqual(result['decoder_classes'], 9)
        self.assertEqual(result['cyrillic_tokens'], 4)


if __name__ == '__main__':
    unittest.main()
