"""Freeze the existing email codec's Unicode data for the Go compatibility adapter.

Normalization is Unicode 3.2, as used by Python's IDNA2003 codec. Python's
stringprep case mapping also uses the interpreter's Unicode lower-case data;
record that version rather than silently changing identity on a runtime upgrade.
"""
import hashlib
import json
import random
import stringprep
import unicodedata
from pathlib import Path

from impactgraph.donors import normalize_email


def ranges(values):
    result = []
    for value in values:
        if result and result[-1][1] + 1 == value:
            result[-1][1] = value
        else:
            result.append([value, value])
    return result


def export():
    ucd = unicodedata.ucd_3_2_0
    mapping, classes, compositions = {}, {}, {}
    prohibited, right, left, whitespace = [], [], [], []
    for code in range(0x110000):
        char = chr(code)
        if 0xD800 <= code <= 0xDFFF:
            continue
        folded = "" if stringprep.in_table_b1(char) else stringprep.map_table_b2(char)
        decomposed = ucd.normalize("NFKD", folded)
        if decomposed != char and not 0xAC00 <= code < 0xD7A4:
            mapping[code] = decomposed
        if (combining := ucd.combining(char)):
            classes[code] = combining
        parts = ucd.decomposition(char).split()
        if len(parts) == 2 and not parts[0].startswith("<"):
            pair = [int(part, 16) for part in parts]
            if ucd.normalize("NFC", "".join(chr(part) for part in pair)) == char:
                compositions[(pair[0] << 21) | pair[1]] = code
        if any(fn(char) for fn in (stringprep.in_table_c12, stringprep.in_table_c22,
                                  stringprep.in_table_c3, stringprep.in_table_c4,
                                  stringprep.in_table_c5, stringprep.in_table_c6,
                                  stringprep.in_table_c7, stringprep.in_table_c8,
                                  stringprep.in_table_c9)):
            prohibited.append(code)
        if stringprep.in_table_d1(char):
            right.append(code)
        if stringprep.in_table_d2(char):
            left.append(code)
        if char.isspace():
            whitespace.append(code)
    root = Path(__file__).resolve().parents[2] / "donor-api"
    table = {"normalization_version": "3.2.0", "case_version": unicodedata.unidata_version,
             "mapping": mapping, "classes": classes, "compositions": compositions,
             "prohibited": ranges(prohibited), "right": ranges(right), "left": ranges(left),
             "whitespace": ranges(whitespace)}
    (root / "internal/identity/tables.json").write_text(json.dumps(table, separators=(",", ":")) + "\n")
    inputs = [" A.B+Tag@EXAMPLE.ORG ", "a@faß.de", "a@σς.gr", "a@bücher.de", "a@例え.テスト",
              "a@a\u2024éb.com", "a@\u200dexample.com", "a@한글.kr", "a@\u1100\u1161.kr",
              "a@😀.com", "a@ＡＢＣ.com", "a@ab\u05d0.com", "a@\u05d0\u05d1.com",
              "a@xn--.com", "a@example.org.", "a@-bad.com", "a..b@example.org",
              "é@example.org", "\x1ca@example.org\x1f", "a@" + "é" * 64 + ".com",
              "a@a" + "\u0300" * 32 + ".com", "a@\u2f868.com"]
    rng = random.Random(1700000000)
    alphabet = "aAéÅßς\u0300\u0301\u0315\u0327\u0345\u1100\u1161\u11a8한\u05d0\u05d1\u200c\u200d\u2024😀"
    inputs.extend("x@" + "".join(rng.choice(alphabet) for _ in range(rng.randrange(1, 12))) + ".org"
                  for _ in range(500))
    cases = []
    for value in inputs:
        try:
            expected = normalize_email(value)
        except ValueError:
            expected = None
        cases.append({"input": value, "expected": expected})
    digest = hashlib.sha256()
    for code in range(0x110000):
        if 0xD800 <= code <= 0xDFFF:
            continue
        try:
            value = normalize_email("x@" + chr(code) + ".org")
        except ValueError:
            value = "!"
        digest.update((value + "\n").encode())
    fixture = {"case_version": unicodedata.unidata_version, "scalar_digest": digest.hexdigest(), "cases": cases}
    (root / "testdata/identity.json").write_text(json.dumps(fixture, indent=2) + "\n")


if __name__ == "__main__":
    export()
