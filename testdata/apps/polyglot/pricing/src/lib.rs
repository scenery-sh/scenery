use std::io::Cursor;
use std::time::Duration;

use scenerycontract::{
    DownloadInput, DownloadOutcome, NativeClient, NativeClientOutcome, PricingConstructorInput,
    PricingService, QuoteInput, QuoteOutcome, QuoteResult, SettleInput, SettleOutcome, sdk,
};

pub struct Service {
    native: NativeClient,
}

pub fn new_service(input: PricingConstructorInput) -> sdk::Result<Service> {
    Ok(Service {
        native: input.native,
    })
}

impl PricingService for Service {
    async fn quote(&self, context: sdk::Context, input: QuoteInput) -> sdk::Result<QuoteOutcome> {
        if let Some(delay) = input.delay_ms {
            let mut call = DelayedCall {
                amount: input.amount.0,
                completed: false,
            };
            eprintln!("delayed quote {} started", call.amount);
            sdk::sleep(Duration::from_millis(delay.clamp(1, 9_000) as u64)).await;
            call.completed = true;
        }
        let NativeClientOutcome::Ok(native) = self.native.invoke(&context, sdk::Unit {}).await?;
        Ok(QuoteOutcome::Ok(QuoteResult {
            amount: input.amount,
            note: input.note,
            vendor: native.vendor,
        }))
    }

    async fn download(
        &self,
        _: sdk::Context,
        _: DownloadInput,
    ) -> sdk::Result<(DownloadOutcome, Option<sdk::Stream>)> {
        let bytes = vec![0x5a; 64 * 1024];
        let size = bytes.len() as u64;
        let outcome = DownloadOutcome::Ok(sdk::ContractBytes(String::new()));
        Ok((
            outcome,
            Some(sdk::Stream {
                reader: Box::pin(Cursor::new(bytes)),
                size,
            }),
        ))
    }

    async fn settle(
        &self,
        context: sdk::Context,
        input: SettleInput,
    ) -> sdk::Result<SettleOutcome> {
        if context.invocation.execution_id.is_none() {
            return Err("missing execution identity".into());
        }
        if let Some(delay) = input.delay_ms {
            eprintln!("settlement {} started", input.id);
            sdk::sleep(Duration::from_millis(delay.clamp(1, 5_000) as u64)).await;
        }
        Ok(SettleOutcome::Ok(sdk::Unit {}))
    }
}

struct DelayedCall {
    amount: i64,
    completed: bool,
}

impl Drop for DelayedCall {
    fn drop(&mut self) {
        if !self.completed {
            eprintln!("delayed quote {} canceled", self.amount);
        }
    }
}
