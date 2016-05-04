package runtime

import "unsafe"

func osinit() {
	ncpu = 1

	atmaninit()
}

func sigpanic() {}

func crash() {
	*(*int32)(nil) = 0
}

func goenvs() {}

//go:nowritebarrier
func newosproc(mp *m, stk unsafe.Pointer) {
	mp.tls[0] = uintptr(mp.id) // so 386 asm can find it
	if false {
		print("newosproc stk=", stk, " m=", mp, " g=", mp.g0, " id=", mp.id, "/", mp.tls[0], " ostk=", &mp, "\n")
	}

	taskcreate(
		unsafe.Pointer(mp),
		unsafe.Pointer(mp.g0),
		unsafe.Pointer(funcPC(mstart)),
		stk,
	)

	taskyield()
}

func resetcpuprofiler(hz int32) {}

func minit() {
	// println("minit()")
}

//go:nosplit
func unminit() {
	// println("unminit()")
}

//go:nosplit
func mpreinit(mp *m) {
	// print("mpreinit(", unsafe.Pointer(mp), ")", "\n")
}

//go:nosplit
func msigsave(mp *m) {
	// print("msigsave(", unsafe.Pointer(mp), ")", "\n")
}

//go:nosplit
func msigrestore(mp *m) {}

//go:nosplit
func sigblock() {}

//go:nosplit
func osyield() {
	taskyield()
}

// Create a semaphore, which will be assigned to m->waitsema.
// The zero value is treated as absence of any semaphore,
// so be sure to return a non-zero value.
//go:nosplit
func semacreate() uintptr {
	return 1
}

// If ns < 0, acquire m->waitsema and return 0.
// If ns >= 0, try to acquire m->waitsema for at most ns nanoseconds.
// Return 0 if the semaphore was acquired, -1 if interrupted or timed out.
//go:nosplit
func semasleep(ns int64) int32 {
	_g_ := getg()
	var ret int32

	systemstack(func() {
		var (
			addr   = &_g_.m.waitsemacount
			waiter = &taskcurrent.semawaiter
			s      = &sleeptable[sleeptablekey(addr)]
		)

		waiter.addr = addr
		waiter.up = false

		kprintString("Task[")
		kprintUint(uint64(taskcurrent.ID))
		kprintString("] semasleep(")
		kprintInt(ns)
		kprintString(") on ")
		kprintPointer(unsafe.Pointer(addr))
		kprintString("\n")

		s.lock()

		if atomicload(addr) > 0 {
			xadd(addr, -1)
			s.unlock()

			ret = 0
			return
		}

		s.add(waiter)

		for !waiter.up {
			s.unlock()
			ns = tasksleep(ns)
			s.lock()

			if ns == 0 {
				break
			}
		}

		if !waiter.up {
			s.remove(waiter)
		}

		s.unlock()

		if waiter.up {
			ret = 0
			return
		}

		ret = -1
	})

	return ret
}

// Wake up mp, which is or will soon be sleeping on mp->waitsema.
//go:nosplit
func semawakeup(mp *m) {
	var (
		addr = &mp.waitsemacount
		s    = &sleeptable[sleeptablekey(addr)]
	)

	kprintString("Task[")
	kprintUint(uint64(taskcurrent.ID))
	kprintString("] semawakeup() on")
	kprintPointer(unsafe.Pointer(addr))
	kprintString("\n")

	s.lock()

	waiter := s.removeWaiterOn(addr)
	if waiter == nil {
		xadd(addr, 1)
	} else {
		waiter.up = true
		taskwake(waiter.task)
	}

	s.unlock()
}

var (
	sleeptable [512]sema
	sleeplocks [512]qlock
)

func sleeptablekey(addr *uint32) int {
	a := uintptr(unsafe.Pointer(addr))
	return int(a & 511) // TODO: hash address
}

type qlock struct {
	owner   *Task
	waiting TaskList
}

func (l *qlock) lock() {
	if l.owner == nil {
		l.owner = taskcurrent
		return
	}

	l.waiting.Add(taskcurrent)
	taskswitch()
}

func (l *qlock) unlock() {
	ready := l.waiting.Head
	l.owner = ready

	if ready != nil {
		l.waiting.Remove(ready)
		taskready(ready)
	}
}

type sema struct {
	*qlock

	head, tail *semawaiter
}

func (s *sema) removeWaiterOn(addr *uint32) *semawaiter {
	for w := s.head; w != nil; w = w.next {
		if w.addr != addr {
			continue
		}

		s.remove(w)
		return w
	}

	return nil
}

func (s *sema) remove(w *semawaiter) {
	if w.prev != nil {
		w.prev.next = w.next
	} else {
		s.head = w.next
	}

	if w.next != nil {
		w.next.prev = w.prev
	} else {
		s.tail = w.prev
	}
}

func (s *sema) add(w *semawaiter) {
	if s.tail != nil {
		s.tail.next = w
		w.prev = s.tail
	} else {
		s.head = w
		w.prev = nil
	}

	s.tail = w
	w.next = nil
}

type semawaiter struct {
	addr *uint32
	task *Task
	up   bool

	next, prev *semawaiter
}

func init() {
	for i := 0; i < 512; i++ {
		sleeptable[i].qlock = &sleeplocks[i]
	}
}
