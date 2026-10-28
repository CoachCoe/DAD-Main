-- Pausing a gift is out of scope (PRD §5.2, §14.2): recurring gifts are
-- cancelled, not paused. donor_resumptions references donor_pauses, so it is
-- dropped first.
DROP TABLE donor_resumptions;
DROP TABLE donor_pauses;
