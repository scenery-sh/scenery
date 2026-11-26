import unittest

from scenerycontract import ConstructorInput, Context, EchoInput, EchoOk, clients
from service import new_service


class EchoServiceTest(unittest.TestCase):
    def test_echo_preserves_int64_and_note(self) -> None:
        svc = new_service(ConstructorInput(clients=clients, config=None, databases={}))
        outcome = svc.echo(Context.__new__(Context), EchoInput(amount=9007199254740993, note="py"))
        self.assertIsInstance(outcome, EchoOk)
        self.assertEqual(outcome.value.amount, 9007199254740993)
        self.assertEqual(outcome.value.note, "py")


if __name__ == "__main__":
    unittest.main()
