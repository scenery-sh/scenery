import unittest

from scenerycontract import ConstructorInput, Context, PyQuoteInput, PyQuoteNegativeAmount, clients
from service import new_service


class PythonServiceTest(unittest.IsolatedAsyncioTestCase):
    async def test_uses_generated_negative_amount_outcome(self) -> None:
        svc = new_service(
            ConstructorInput(
                clients=clients,
                config=None,
                databases={"database": "postgres://localhost/unused"},
            )
        )
        outcome = await svc.quote(Context.__new__(Context), PyQuoteInput(amount=-1))
        self.assertIsInstance(outcome, PyQuoteNegativeAmount)
        self.assertEqual(outcome.kind, "error")
        self.assertEqual(outcome.name, "negative_amount")
        await svc.stop()


if __name__ == "__main__":
    unittest.main()
