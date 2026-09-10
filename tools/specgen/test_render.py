"""Behavioral specification integrity checks; no product behavior is claimed."""
import copy
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import render

class SpecificationIntegrity(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.requirements=json.loads((render.DOC/'requirements.json').read_text())
        cls.acceptance=json.loads((render.DOC/'acceptance.json').read_text())
        cls.adapters=json.loads((render.DOC/'adapter-schemas.json').read_text())

    def test_missing_dependency_schema_refuses_render(self):
        with self.assertRaisesRegex(ValueError,'unresolved schema'):
            render.closure({'$ref':'#/$defs/MissingOwnerType'},render.D)

    def test_transport_name_collision_refuses_render(self):
        ops=copy.deepcopy(render.OPS)
        public=[o for o in ops if o['visibility']=='public']
        public[1]['mcp']=public[0]['mcp']
        with patch.object(render,'OPS',ops), self.assertRaisesRegex(AssertionError,'mapping collision'):
            render.validate(self.requirements,self.acceptance,self.adapters)

    def test_overlapping_concurrent_roots_refuse_render(self):
        packages=copy.deepcopy(render.P)
        packages[1]['path']=packages[0]['path']+'/child'
        with patch.object(render,'P',packages), self.assertRaisesRegex(AssertionError,'overlapping'):
            render.validate(self.requirements,self.acceptance,self.adapters)

    def test_cyclic_package_imports_refuse_render(self):
        packages=copy.deepcopy(render.P)
        packages[0]['imports']=[packages[1]['name']]
        packages[1]['imports']=[packages[0]['name']]
        with patch.object(render,'P',packages), self.assertRaisesRegex(AssertionError,'import cycle'):
            render.validate(self.requirements,self.acceptance,self.adapters)

    def test_generation_without_rfc_and_detection_of_tampered_prompt(self):
        with tempfile.TemporaryDirectory(prefix='zatiti-spec-') as tmp:
            root=Path(tmp)
            (root/'tools/specgen').mkdir(parents=True)
            (root/'docs/implementation').mkdir(parents=True)
            for name in ('model.py','packages.py','render.py'):
                shutil.copy2(render.ROOT/'tools/specgen'/name,root/'tools/specgen'/name)
            for name in ('contracts.md','requirements.json','acceptance.json','adapter-schemas.json'):
                shutil.copy2(render.DOC/name,root/'docs/implementation'/name)
            self.assertFalse((root/'docs/rfc.md').exists())
            cmd=[sys.executable,'-S',str(root/'tools/specgen/render.py')]
            generated=subprocess.run(cmd,capture_output=True,text=True)
            self.assertEqual(generated.returncode,0,generated.stdout+generated.stderr)
            checked=subprocess.run(cmd+['--check'],capture_output=True,text=True)
            self.assertEqual(checked.returncode,0,checked.stdout+checked.stderr)
            for p in render.P:
                local=root/p['path']/'AGENTS.md'
                self.assertEqual(local.read_bytes(),(render.ROOT/p['path']/'AGENTS.md').read_bytes())
            victim=root/'internal/effects/AGENTS.md'
            victim.write_text(victim.read_text().replace('outcome_unknown','succeeded',1))
            drift=subprocess.run(cmd+['--check'],capture_output=True,text=True)
            self.assertEqual(drift.returncode,1)
            self.assertIn('internal/effects/AGENTS.md',drift.stdout)

    def test_every_source_requirement_is_embedded_in_owner_prompt(self):
        packages={p['name']:p for p in render.P}
        cache={}
        for r in self.requirements:
            name=r['owner']
            if name not in cache:cache[name]=(render.ROOT/packages[name]['path']/'AGENTS.md').read_text()
            self.assertIn(r['text'],cache[name],r['id'])

if __name__=='__main__':unittest.main()
