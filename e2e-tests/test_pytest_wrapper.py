import pytest
from lib.bash_wrapper import run_bash_test


def test_bash_wrapper(request: pytest.FixtureRequest) -> None:
    """Run a bash test script via pytest wrapper."""
    test_name = request.config.getoption("--test-name")
    if not test_name:
        pytest.skip("No --test-name provided")
    run_bash_test(test_name)
