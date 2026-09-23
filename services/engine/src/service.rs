//! tonic service implementation.

use crate::{
    pb::{engine_server::Engine, HealthRequest, HealthResponse, SimulateRequest, SimulateResponse},
    sim::{self, SimError},
};
use tonic::{Request, Response, Status};

/// gRPC handler; stateless apart from the gas ceiling.
#[derive(Debug, Clone)]
pub struct EngineSvc {
    max_gas: u64,
}

impl EngineSvc {
    /// Creates a service that rejects requests above `max_gas`.
    pub fn new(max_gas: u64) -> Self {
        Self { max_gas }
    }
}

fn to_status(e: &SimError) -> Status {
    match e {
        SimError::Invalid(_) => Status::invalid_argument(e.to_string()),
        SimError::GasTooHigh(_) => Status::resource_exhausted(e.to_string()),
        SimError::Rejected(_) => Status::failed_precondition(e.to_string()),
    }
}

#[tonic::async_trait]
impl Engine for EngineSvc {
    async fn simulate(&self, req: Request<SimulateRequest>) -> Result<Response<SimulateResponse>, Status> {
        sim::simulate(req.get_ref(), self.max_gas).map(Response::new).map_err(|e| to_status(&e))
    }

    async fn health(&self, _: Request<HealthRequest>) -> Result<Response<HealthResponse>, Status> {
        Ok(Response::new(HealthResponse { version: env!("CARGO_PKG_VERSION").to_string() }))
    }
}
