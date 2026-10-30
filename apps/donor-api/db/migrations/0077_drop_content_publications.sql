-- The spreadsheet-style content publication was replaced by the initiative
-- editor (migrations 0061 and 0063 copied everything it held), and nothing
-- reads or writes these tables any more. Dropping them discards the
-- publication history by design; the board-approval source documents it
-- referenced stay in donor_content_sources.
DROP TABLE IF EXISTS donor_content_head;
DROP TABLE IF EXISTS donor_content_publications;
