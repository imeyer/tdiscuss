-- Add is_blocked field to member table to support blocking members instead of deleting them
ALTER TABLE member ADD COLUMN is_blocked boolean DEFAULT false;

-- Create index for is_blocked to improve query performance
CREATE INDEX member_is_blocked_index ON member(is_blocked);