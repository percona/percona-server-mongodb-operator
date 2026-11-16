import pytest
from lib.allure_support import set_title
from lib.bash_wrapper import run_bash_test


def test_bash_wrapper(request: pytest.FixtureRequest, bash_test_cleanup: None) -> None:
    """Run a bash test script via pytest wrapper."""
    test_name = request.config.getoption("--test-name")
    if not test_name:
        pytest.skip("No --test-name provided")
    set_title(test_name)
    run_bash_test(test_name)
