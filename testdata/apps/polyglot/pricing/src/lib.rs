use scenerycontract::{
    NativeClient, NativeClientOutcome, PricingConstructorInput, PricingService, QuoteInput,
    QuoteOutcome, QuoteResult, sdk,
};

pub struct Service {
    native: NativeClient,
}

pub fn new_service(input: PricingConstructorInput) -> sdk::Result<Service> {
    Ok(Service { native: input.native })
}

impl PricingService for Service {
    async fn quote(&self, context: sdk::Context, input: QuoteInput) -> sdk::Result<QuoteOutcome> {
        let NativeClientOutcome::Ok(native) = self.native.invoke(&context, sdk::Unit {}).await?;
        Ok(QuoteOutcome::Ok(QuoteResult {
            amount: input.amount,
            note: input.note,
            vendor: native.vendor,
        }))
    }
}
