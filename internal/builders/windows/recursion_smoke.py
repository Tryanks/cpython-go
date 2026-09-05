import json


root = []
cursor = root
for _ in range(20_000):
    child = []
    cursor.append(child)
    cursor = child

try:
    json.dumps(root)
except RecursionError:
    print("recursion-guard-ok")
else:
    raise AssertionError("deep C recursion did not raise RecursionError")
