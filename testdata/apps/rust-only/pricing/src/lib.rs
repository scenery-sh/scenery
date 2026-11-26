use scenerycontract::{
    PricingConstructorInput, PricingService, QuoteInput, QuoteOutcome, QuoteResult, sdk,
};

pub struct Service;

pub fn new_service(_: PricingConstructorInput) -> sdk::Result<Service> {
    Ok(Service)
}

impl PricingService for Service {
    async fn start(&self) -> sdk::Result<()> {
        eprintln!("pricing worker started");
        Ok(())
    }

    async fn stop(&self) -> sdk::Result<()> {
        eprintln!("pricing worker stopped");
        Ok(())
    }

    async fn quote(&self, _: sdk::Context, input: QuoteInput) -> sdk::Result<QuoteOutcome> {
        Ok(QuoteOutcome::Ok(QuoteResult {
            amount: input.amount,
            note: input.note,
        }))
    }
}
