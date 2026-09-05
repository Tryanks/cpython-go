"""Small, dependency-free interpreter benchmark suite.

Each workload is run three times in one process.  The minimum wall-clock
duration is reported to reduce scheduler noise.  Lower is better.

The nbody and Richards workloads are adapted from python/pyperformance
(MIT); nbody ultimately comes from the Computer Language Benchmarks Game.
"""

import json
import math
import re
import sys
import time


REPETITIONS = 3


def measure(name, func):
    samples = []
    result = None
    for _ in range(REPETITIONS):
        start = time.perf_counter()
        result = func()
        samples.append(time.perf_counter() - start)
    if result is None:
        raise RuntimeError("benchmark returned no result: " + name)
    print("%-18s %.6f" % (name, min(samples)), flush=True)


# nbody: pyperformance's five-body benchmark.
PI = 3.14159265358979323
SOLAR_MASS = 4 * PI * PI
DAYS_PER_YEAR = 365.24
NBODY_INITIAL = (
    ((0.0, 0.0, 0.0), (0.0, 0.0, 0.0), SOLAR_MASS),
    ((4.841431442464721, -1.1603200440274284, -0.10362204447112311),
     (0.001660076642744037 * DAYS_PER_YEAR,
      0.007699011184197404 * DAYS_PER_YEAR,
      -0.0000690460016972063 * DAYS_PER_YEAR),
     0.0009547919384243266 * SOLAR_MASS),
    ((8.34336671824458, 4.124798564124305, -0.4035234171143214),
     (-0.002767425107268624 * DAYS_PER_YEAR,
      0.004998528012349172 * DAYS_PER_YEAR,
      0.000023041729757376393 * DAYS_PER_YEAR),
     0.0002858859806661308 * SOLAR_MASS),
    ((12.894369562139131, -15.111151401698631, -0.22330757889265573),
     (0.002964601375647616 * DAYS_PER_YEAR,
      0.0023784717395948095 * DAYS_PER_YEAR,
      -0.000029658956854023756 * DAYS_PER_YEAR),
     0.00004366244043351563 * SOLAR_MASS),
    ((15.379697114850917, -25.919314609987964, 0.17925877295037118),
     (0.0026806777249038932 * DAYS_PER_YEAR,
      0.001628241700382423 * DAYS_PER_YEAR,
      -0.00009515922545197159 * DAYS_PER_YEAR),
     0.000051513890204661145 * SOLAR_MASS),
)


def nbody():
    bodies = [[list(pos), list(vel), mass] for pos, vel, mass in NBODY_INITIAL]
    pairs = [(bodies[i], bodies[j])
             for i in range(len(bodies) - 1)
             for j in range(i + 1, len(bodies))]
    px = py = pz = 0.0
    for _, (vx, vy, vz), mass in bodies:
        px -= vx * mass
        py -= vy * mass
        pz -= vz * mass
    bodies[0][1][:] = (px / SOLAR_MASS, py / SOLAR_MASS, pz / SOLAR_MASS)
    for _ in range(20000):
        for (p1, v1, m1), (p2, v2, m2) in pairs:
            dx, dy, dz = p1[0] - p2[0], p1[1] - p2[1], p1[2] - p2[2]
            mag = 0.01 * (dx * dx + dy * dy + dz * dz) ** -1.5
            b1m, b2m = m1 * mag, m2 * mag
            v1[0] -= dx * b2m
            v1[1] -= dy * b2m
            v1[2] -= dz * b2m
            v2[0] += dx * b1m
            v2[1] += dy * b1m
            v2[2] += dz * b1m
        for pos, (vx, vy, vz), _ in bodies:
            pos[0] += 0.01 * vx
            pos[1] += 0.01 * vy
            pos[2] += 0.01 * vz
    return bodies[0][0]


# Richards: classic task-dispatch benchmark used by pyperformance.
I_IDLE, I_WORK, I_HANDLERA, I_HANDLERB, I_DEVA, I_DEVB = range(1, 7)
K_DEV, K_WORK = 1000, 1001


class Packet:
    def __init__(self, link, ident, kind):
        self.link, self.ident, self.kind = link, ident, kind
        self.datum = 0
        self.data = [0] * 4

    def append_to(self, queue):
        self.link = None
        if queue is None:
            return self
        packet = queue
        while packet.link is not None:
            packet = packet.link
        packet.link = self
        return queue


class TaskState:
    def __init__(self, pending=True, waiting=False, holding=False):
        self.packet_pending = pending
        self.task_waiting = waiting
        self.task_holding = holding

    def is_holding_or_waiting(self):
        return self.task_holding or (not self.packet_pending and self.task_waiting)


class Scheduler:
    def __init__(self):
        self.tasks = [None] * 10
        self.task_list = None
        self.hold_count = 0
        self.queue_count = 0

    def schedule(self):
        task = self.task_list
        while task is not None:
            if task.is_holding_or_waiting():
                task = task.link
            else:
                task = task.run()


class Task(TaskState):
    def __init__(self, scheduler, ident, priority, queue, state, record):
        super().__init__(state.packet_pending, state.task_waiting,
                         state.task_holding)
        self.scheduler = scheduler
        self.link = scheduler.task_list
        self.ident, self.priority = ident, priority
        self.input, self.record = queue, record
        scheduler.task_list = self
        scheduler.tasks[ident] = self

    def run(self):
        message = None
        if self.packet_pending and self.task_waiting and not self.task_holding:
            message = self.input
            self.input = message.link
            self.packet_pending = self.input is not None
            self.task_waiting = False
        return self.work(message)

    def wait(self):
        self.task_waiting = True
        return self

    def hold(self):
        self.scheduler.hold_count += 1
        self.task_holding = True
        return self.link

    def release(self, ident):
        task = self.scheduler.tasks[ident]
        task.task_holding = False
        return task if task.priority > self.priority else self

    def queue(self, packet):
        task = self.scheduler.tasks[packet.ident]
        self.scheduler.queue_count += 1
        packet.link = None
        packet.ident = self.ident
        if task.input is None:
            task.input = packet
            task.packet_pending = True
            return task if task.priority > self.priority else self
        packet.append_to(task.input)
        return self


class IdleTask(Task):
    def work(self, packet):
        record = self.record
        record[1] -= 1
        if record[1] == 0:
            return self.hold()
        if record[0] & 1 == 0:
            record[0] //= 2
            return self.release(I_DEVA)
        record[0] = record[0] // 2 ^ 0xD008
        return self.release(I_DEVB)


class WorkerTask(Task):
    def work(self, packet):
        if packet is None:
            return self.wait()
        record = self.record
        record[0] = I_HANDLERB if record[0] == I_HANDLERA else I_HANDLERA
        packet.ident, packet.datum = record[0], 0
        for index in range(4):
            record[1] = record[1] % 26 + 1
            packet.data[index] = ord("A") + record[1] - 1
        return self.queue(packet)


class HandlerTask(Task):
    def work(self, packet):
        record = self.record
        if packet is not None:
            slot = 0 if packet.kind == K_WORK else 1
            record[slot] = packet.append_to(record[slot])
        work = record[0]
        if work is None:
            return self.wait()
        if work.datum >= 4:
            record[0] = work.link
            return self.queue(work)
        device = record[1]
        if device is None:
            return self.wait()
        record[1] = device.link
        device.datum = work.data[work.datum]
        work.datum += 1
        return self.queue(device)


class DeviceTask(Task):
    def work(self, packet):
        if packet is None:
            packet = self.record[0]
            if packet is None:
                return self.wait()
            self.record[0] = None
            return self.queue(packet)
        self.record[0] = packet
        return self.hold()


def richards():
    scheduler = Scheduler()
    IdleTask(scheduler, I_IDLE, 0, None, TaskState(False), [1, 10000])
    queue = Packet(Packet(None, 0, K_WORK), 0, K_WORK)
    WorkerTask(scheduler, I_WORK, 1000, queue,
               TaskState(True, True), [I_HANDLERA, 0])
    queue = Packet(Packet(Packet(None, I_DEVA, K_DEV), I_DEVA, K_DEV),
                   I_DEVA, K_DEV)
    HandlerTask(scheduler, I_HANDLERA, 2000, queue,
                TaskState(True, True), [None, None])
    queue = Packet(Packet(Packet(None, I_DEVB, K_DEV), I_DEVB, K_DEV),
                   I_DEVB, K_DEV)
    HandlerTask(scheduler, I_HANDLERB, 3000, queue,
                TaskState(True, True), [None, None])
    DeviceTask(scheduler, I_DEVA, 4000, None, TaskState(False, True), [None])
    DeviceTask(scheduler, I_DEVB, 5000, None, TaskState(False, True), [None])
    scheduler.schedule()
    result = scheduler.hold_count, scheduler.queue_count
    if result != (9297, 23246):
        raise RuntimeError("Richards validation failed: %r" % (result,))
    return result


REGEX_TEXT = "\n".join(
    "user%d@example.com GET /items/%d status=%s token=%08x"
    % (i % 997, i, ("ok" if i % 7 else "retry"), i * 2654435761 & 0xFFFFFFFF)
    for i in range(6000)
)
REGEXES = (
    re.compile(r"\b[\w.+-]+@[\w.-]+\.com\b"),
    re.compile(r"/items/(\d+)\s+status=(ok|retry)"),
    re.compile(r"token=([0-9a-f]{8})"),
)


def regex_workload():
    total = 0
    for _ in range(10):
        for pattern in REGEXES:
            total += len(pattern.findall(REGEX_TEXT))
    return total


JSON_OBJECT = {
    "meta": {"version": 3, "active": True, "tags": ["go", "python", "ccgo"]},
    "items": [
        {"id": i, "name": "item-%04d" % i, "values": list(range(i % 17)),
         "attrs": {"group": i % 13, "enabled": i % 3 != 0}}
        for i in range(400)
    ],
}


def json_workload():
    checksum = 0
    for _ in range(40):
        encoded = json.dumps(JSON_OBJECT, sort_keys=True, separators=(",", ":"))
        checksum += len(json.loads(encoded)["items"])
    return checksum


WORDS = ("alpha beta gamma delta epsilon zeta eta theta iota kappa "
         "lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega")
WORD_TEXT = " ".join(WORDS.split()[i * 7 % 24] for i in range(12000))


def word_count():
    checksum = 0
    for _ in range(80):
        counts = {}
        for word in WORD_TEXT.split():
            key = word + ":" + str(len(word))
            counts[key] = counts.get(key, 0) + 1
        checksum += sum(counts.values())
    return checksum


def generator_closure():
    def multiplier(factor):
        def apply(value):
            return (value * factor + 17) % 1000003
        return apply

    funcs = [multiplier(i) for i in range(1, 9)]

    def values(limit):
        for i in range(limit):
            yield funcs[i & 7](i)

    return sum(value for value in values(350000) if value & 1)


BENCHMARKS = (
    ("nbody", nbody),
    ("richards", richards),
    ("regex", regex_workload),
    ("json", json_workload),
    ("dict_str", word_count),
    ("generator_closure", generator_closure),
)


def main():
    selected = set(sys.argv[1:])
    for name, function in BENCHMARKS:
        if not selected or name in selected:
            measure(name, function)


if __name__ == "__main__":
    main()
