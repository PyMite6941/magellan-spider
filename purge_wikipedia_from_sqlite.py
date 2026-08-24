#!/usr/bin/env python3
"""
Script to purge Wikipedia URLs from the SQLite database (magellan.sq3).

This script:
1. Connects to the SQLite database (magellan.sq3).
2. Identifies and deletes all rows in the 'pages' table where the URL contains 'wikipedia.org'.
3. Logs the number of rows deleted.

Prerequisites:
- Install required packages: pip install sqlite3
- Ensure the SQLite database file exists at the specified path.
"""

import os
import logging
import sqlite3

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# SQLite database path
sqlite_db_path = "magellan.sq3"  # Relative to the script's directory


def validate_database():
    """Validate that the SQLite database exists."""
    if not os.path.exists(sqlite_db_path):
        raise FileNotFoundError(f"SQLite database not found at: {sqlite_db_path}")


def purge_wikipedia_urls():
    """Delete all Wikipedia URLs from the SQLite database."""
    try:
        # Connect to the SQLite database
        logger.info(f"Connecting to SQLite database at {sqlite_db_path}...")
        conn = sqlite3.connect(sqlite_db_path)
        cursor = conn.cursor()

        logger.info("Successfully connected to SQLite database.")

        # Check if the 'pages' table exists
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table' AND name='pages';")
        if not cursor.fetchone():
            logger.error("Table 'pages' not found in the database.")
            return

        # Count Wikipedia URLs before deletion
        cursor.execute("SELECT COUNT(*) FROM pages WHERE url LIKE '%wikipedia.org%';")
        count_before = cursor.fetchone()[0]
        logger.info(f"Found {count_before} Wikipedia URLs to purge.")

        if count_before == 0:
            logger.info("No Wikipedia URLs found. Nothing to purge.")
            return

        # Delete Wikipedia URLs
        cursor.execute("DELETE FROM pages WHERE url LIKE '%wikipedia.org%';")

        # Commit the transaction
        conn.commit()

        # Count Wikipedia URLs after deletion
        cursor.execute("SELECT COUNT(*) FROM pages WHERE url LIKE '%wikipedia.org%';")
        count_after = cursor.fetchone()[0]

        logger.info(f"Purged {count_before - count_after} Wikipedia URLs from the database.")

    except sqlite3.Error as e:
        logger.error(f"SQLite error: {e}")
        raise
    except Exception as e:
        logger.error(f"Error during Wikipedia URL purge: {e}")
        raise
    finally:
        if 'conn' in locals():
            conn.close()
            logger.info("Database connection closed.")


def main():
    """Main function to orchestrate the purge process."""
    try:
        validate_database()
        purge_wikipedia_urls()
        logger.info("Wikipedia URL purge completed successfully!")
    except Exception as e:
        logger.error(f"Process failed: {e}")
        raise


if __name__ == "__main__":
    main()