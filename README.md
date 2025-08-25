# shopnexus-remastered

## My code, my rules...

### Database
- Always use table per type (TPT) in database design
- Audit snapshot for tax authority and transaction dispute purpose


### Go

#### General
- No use orchestration patterns, use choreography instead.
- Use choreography pattern with compensating transactions to handle failures gracefully.
- Always use events to communicate between services to microservice friendly and avoid tight coupling.

#### Early stage:
- Use models generated from sqlc as much as possible to coupling data with database schema, easy for development
- Only create custom model for DTOs only (response data) and some custom types that are not directly related to database schema

#### Later stage:
- Create our own domain models to decouple from database schema


### Biz
- Tag only for SEO purpose, use category for product grouping instead.
- Handle the problem "Slowly Changing Dimension (SCD)" in database design (financial transactions related)