See @README for project overview

# Plans for improving the project
The `agents\plans` directory contains plans on how the project can improve and the current state of implementing each feature.
These should be kept up to date as each feature progresses.
- agents\plans\\\<feature\>\PLAN.md

# Project core values
Keep the following core values in mind when developing the project.
- Efficiency 
  - minimize duplication of code
  - minimize large dependencies
  - ensure functions are called only when needed
- Maintainability 
  - avoid hardcoded values outside of defaults
  - add configration variables for features that could be tweaked
  - keep configuration variables grouped together
  - add tests for critical functions
- Scalability 
  - consider solutions that scale well
  - add retry, backoff, and error checking logic where necessary

# Testing
Run `go test ./...` from the root project directory and verify all tests pass.
