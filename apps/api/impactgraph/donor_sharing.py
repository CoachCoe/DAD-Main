"""Donor-controlled public names, link sharing, and on-screen IRL codes."""

import qrcode
from qrcode.image.svg import SvgPathFillImage
from sqlalchemy import select

from .donors import _hash, authenticated_donor
from .persistence import DonorRecord, ReferralTouchRecord


def sharing_details(session, token, origin):
    donor = authenticated_donor(session, token)
    link = f"{origin}/r/{donor.referral_code}" if donor.referral_code and donor.public_name else None
    return {"public_name": donor.public_name, "link": link,
            "irl_link": f"{link}?via=irl" if link else None}


def captured_name(session, visitor_token):
    if not visitor_token:
        return None
    return session.scalar(select(DonorRecord.public_name).join(
        ReferralTouchRecord, ReferralTouchRecord.referrer_id == DonorRecord.id,
    ).where(ReferralTouchRecord.token_hash == _hash(visitor_token),
            DonorRecord.disabled.is_(False)))


def referral_svg(url):
    # Plain black modules on white with a full quiet zone; no logo or styling that
    # reduces scan reliability. Only the server-built IRL URL enters the QR encoder.
    code = qrcode.QRCode(error_correction=qrcode.constants.ERROR_CORRECT_H, border=4, box_size=10)
    code.add_data(url)
    code.make(fit=True)
    return code.make_image(image_factory=SvgPathFillImage).to_string()
