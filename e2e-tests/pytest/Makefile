POETRY_VENV_DIR=.poetryvenv
POETRY_PIP=$(POETRY_VENV_DIR)/bin/pip
POETRY_PYTHON=$(POETRY_VENV_DIR)/bin/python
POETRY=$(POETRY_VENV_DIR)/bin/poetry

.poetryvenv:
	python -m venv $(POETRY_VENV_DIR)

poetry: .poetryvenv
	$(POETRY_PIP) install -U pip setuptools
	$(POETRY_PIP) install -U poetry

dependencies:
	$(POETRY) install
