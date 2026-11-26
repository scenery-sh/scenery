from scenerycontract import ConstructorInput, Context, EchoInput, EchoOk, EchoOutcome


class EchoService:
    def echo(self, ctx: Context, input: EchoInput) -> EchoOutcome:
        return EchoOk(value=input)


def new_service(input: ConstructorInput) -> EchoService:
    return EchoService()
