"""Verify PUBLIC Go-generated fixtures with the production Python crypto libraries."""
import base64
import json
import sys
from pathlib import Path

from argon2 import PasswordHasher
from cryptography.fernet import Fernet


def verify(path):
    data = json.loads(Path(path).read_text())
    assert PasswordHasher().verify(data["go_argon2id"], "test-only-password-é")
    cipher = Fernet(base64.urlsafe_b64encode(bytes(range(32))))
    assert cipher.decrypt(data["go_fernet_token"].encode()) == "public test payload: é".encode()


if __name__ == "__main__":
    verify(sys.argv[1])
