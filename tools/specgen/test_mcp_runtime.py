"""Runtime MCP mirrors must retain the authored revision's schemas."""
import subprocess
import sys
import unittest
from pathlib import Path

class MCPRuntimeSchemaTests(unittest.TestCase):
    def test_runtime_mirrors_match_authored_contract(self):
        script = Path(__file__).with_name("sync_mcp_runtime.py")
        subprocess.run([sys.executable, str(script), "--check"], check=True)
