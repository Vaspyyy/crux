from store import Store, count


def test_count():
    assert count([1, 2]) == 2


class TestStore:
    def test_missing(self):
        Store("/nonexistent").load("x")
