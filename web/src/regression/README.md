# Work notes

* "ensure" functions should go through API (speed)
  * have other tests to test UI functionality; don't rely on "ensure" functions to implicitly test some UI workflows
  * they should be idempotent and be super quick if the condition is already satisfied

* make tests as fast as possible; this improves development time
* make tests easily runnable in piecemeal fashion
* make errors easy; make it clear how to run just the failing tests
* strong stylistic conventions in test files (let these reveal themselves over time)

## Impl plan

### Phase 1: non-scale tests (tests that can be run against any instance of Sourcegraph)
1. Refine and add to search tests
2. Core functionality
3. Write up RFC

### Phase 1 (continued)

3. Onboarding
4. Saved searches
5. Code navigation
  - should only test UI functionality; should rely on non-UI integration tests for language-specific stuff
6. Auth
  - will depend on external services
  - interaction with external services should be through API only
7. External services
  - test repo-updater, too (tests may need to be long-running); consult with core services about how to do this
8. Config variance + critical config
9. Site admin
  

### Phase 2: scale tests
* Way to check that conditions hold; may need to wait longer
  * ...this should be in theory the same mechanism as the non-scale tests (just with longer timeouts, may want to report progress)
* Add search scalability tests

### Phase 3: customer-specific tests
* These cannot live in OSS repository, but should share code
* Might be more custom, one-off scripts (though it would be nice if they weren't too hacky, but right them to a purpose, instead of for generic case)

### Phase 4: documentation + process changes
* Document how to create new regression test suites
* Release captain should enforce that major new, non-experimental features are adequately regression-tested
* Determine how to run regression tests on a regular basis (e.g., nightly cron job against both fresh and long-running dogfood instances?)

### Out of scope
* Browser extension
* Extensions
  - Each extension we want to support publicly should have a regression test suite
