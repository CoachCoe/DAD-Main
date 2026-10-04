"""Generate PUBLIC test-only credential vectors. These are never deployment secrets."""
import base64
import hashlib
import json
from pathlib import Path

from argon2 import PasswordHasher
from cryptography.fernet import Fernet
from cryptography.hazmat.primitives.hashes import SHA1
from cryptography.hazmat.primitives.twofactor.totp import TOTP

if __name__ == "__main__":
    key = base64.urlsafe_b64encode(bytes(range(32))).decode()
    password = "test-only-password-\u00e9"
    result = {
        "notice": "PUBLIC TEST VECTORS ONLY; never configure a deployment with these values",
        "fernet_key": key,
        "fernet_plaintext": "public test payload: \u00e9",
        "python_fernet_token": Fernet(key.encode()).encrypt_at_time("public test payload: \u00e9".encode(), 1700000000).decode(),
        "password": password,
        "python_argon2id": PasswordHasher().hash(password),
        "otp_secret": base64.b32encode(bytes(range(20))).decode(),
        "otp_time": 1700000000,
        "otp_code": TOTP(bytes(range(20)), 6, SHA1(), 30).generate(1700000000).decode(),
        "checkout": [],
    }
    for visitor in (None, "", "ascii-token", "é<>&\n\t\b\f\r\\\"😀\x7f"):
        data = ["donor@example.org", 3000, "MONTHLY", visitor]
        preimage = json.dumps(data, separators=(",", ":"))
        result["checkout"].append({"visitor": visitor, "preimage": preimage,
                                   "hash": hashlib.sha256(preimage.encode()).hexdigest()})
    target = Path(__file__).resolve().parents[2] / "donor-api/testdata/credentials.json"
    target.write_text(json.dumps(result, indent=2) + "\n")
