"""Test the displayed setup link in a terminal and in plain output."""
import io
from pathlib import Path
import unittest
from unittest.mock import patch

from configure import show_setup_summary


class InstallSummaryTests(unittest.TestCase):
    code = 'a' * 32
    url = 'http://filehub.test:8080/setup#code=' + code
    state = Path('/var/lib/filehub-test')

    def render(self, *, terminal, started=True, term='xterm-256color'):
        with patch('sys.stdout', new_callable=io.StringIO) as output:
            with patch.object(output, 'isatty', return_value=terminal), patch.dict('os.environ', {'TERM': term}):
                show_setup_summary('http://filehub.test:8080/', self.code, self.state, started=started)
            return output.getvalue()

    def test_terminal_link_targets_and_displays_the_same_url(self):
        output = self.render(terminal=True)
        self.assertIn(f'\x1b]8;;{self.url}\x1b\\\x1b[1;36m{self.url}\x1b[0m\x1b]8;;\x1b\\\n', output)
        self.assertIn('FileHub установлен', output)

    def test_noninteractive_output_keeps_a_copyable_url(self):
        for output in (self.render(terminal=False), self.render(terminal=True, term='dumb')):
            with self.subTest(output=output):
                self.assertNotIn('\x1b', output)
                self.assertIn(self.url, [line.strip() for line in output.splitlines()])
                self.assertIn(str(self.state / 'setup-code.json'), output)

    def test_generated_configuration_describes_when_to_open_the_link(self):
        output = self.render(terminal=False, started=False)
        self.assertIn('Конфигурация FileHub готова', output)
        self.assertIn('после запуска служб', output)
        self.assertNotIn('FileHub установлен', output)
