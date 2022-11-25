from math import ceil

def get_ranges(_online_count):
    online = ceil(_online_count / 100.0) * 100
    ranges = []
    for i in range(1, int(online / 100) + 1):
        min = i * 100
        max = min + 99
        ranges.append([min, max])
    return ranges

def get_current_ranges(ranges):
    try:
        current = [[0, 99]]
        current.append(ranges.pop(0))
        try:
            current.append(ranges.pop(0))
        except IndexError:
            pass
        return current
    except:
        return

ranges = get_ranges(5000)
print(ranges)
current = [0, 99]
while current:
    current = get_current_ranges(ranges)
    print(current)