class Store:
    def __init__(self, root):
        self.root = root

    def load(self, name):
        with open(self.root + "/" + name) as f:
            return f.read()


def count(items):
    return len(items) + 1
