package runtime

const (
	cellSize       = 8
	clearInterNano = 1e9 * 120
)

var (
	gpCells       [cellSize]gpCell = [cellSize]gpCell{}
	scanGLastTime int64
	curIdx        = 0
)

//
type gpCell struct {
	infos [][4]int64
	lock  mutex
}

//
func (c *gpCell) add(gid, pid int64) {
	lock(&c.lock)
	if c.infos == nil {
		c.infos = [][4]int64{}
	}
	c.infos = append(c.infos, [4]int64{gid, pid, nanotime(), 1})
	unlock(&c.lock)
}

//
func (c *gpCell) get(gid int64) int64 {
	lock(&c.lock)
	if c.infos == nil {
		unlock(&c.lock)
		return -1
	}

	ret := int64(-1)
	nano := int64(0)

	for _, v := range c.infos {
		if v[0] == gid {
			if nano < v[2] {
				ret = v[1]
				nano = v[2]
			} else {
				v[3] = 0
			}
		}
	}
	unlock(&c.lock)
	return ret
}

//
func Getgpid(gid int64, rlt []int64) int {
	if len(rlt) < 1 {
		return 0
	}
	i := 1

	rlt[0] = gid

	for i < len(rlt) && gid > 0 {
		idx := gid % cellSize
		gid = gpCells[idx].get(gid)
		rlt[i] = gid
		if gid > 0 {
			i++
		}
	}
	return i
}

//
func scanGCellsValid() {
	now := nanotime()
	if now-scanGLastTime < clearInterNano {
		return
	}
	scanGLastTime = now

	idx := curIdx % cellSize
	curIdx++

	lock(&gpCells[idx].lock)
	if gpCells[idx].infos == nil {
		unlock(&gpCells[idx].lock)
		return
	}

	for k := 0; k < len(gpCells[idx].infos); k++ {
		if gpCells[idx].infos[k][3] == 0 {
			// if now-gpCells[idx].infos[k][2] > 1e3 {
			gpCells[idx].infos = append(gpCells[idx].infos[:k], gpCells[idx].infos[k+1:]...)
			k--
		}
	}

	unlock(&gpCells[idx].lock)
}

func DumpGpCells(fun func(gid, pid, nano, val int64)) {
	for i := 0; i < cellSize; i++ {
		lock(&gpCells[i].lock)
		if gpCells[i].infos != nil {
			for _, v := range gpCells[i].infos {
				fun(v[0], v[1], v[2], v[3])
			}
		}
		unlock(&gpCells[i].lock)
	}
}

//
func onGStartHook(ng, pg *g) {
	idx := ng.goid % cellSize
	gpCells[idx].add(ng.goid, pg.goid)
	scanGCellsValid()
}

//
func Getgid() int64 {
	_g_ := getg()
	return _g_.goid
}

// ----------------------------------------------------------------
//
/*
type gMap struct {
	mm   map[int64]int64
	lock mutex
}

var gmap gMap

func onGStartHook(newg, oldg *g) {
	newGId, curGId := newg.goid, oldg.goid
	// newGId, curGId := newg.forTrace, oldg.forTrace
	lock(&gmap.lock)

	// if gmap.gids == nil {
	// gmap.gids = [][2]int64{}
	// }
	// gmap.gids = append(gmap.gids, [2]int64{newGId, curGId})

	if gmap.mm == nil {
		gmap.mm = make(map[int64]int64)
	}
	gmap.mm[newGId] = curGId
	unlock(&gmap.lock)
}

func onGStopHook(gg *g) {
	print("g stop")
}

//
func Getgpid(gid int64, rlt []int64) int {

	lock(&gmap.lock)

	ok := true
	i := 1
	rlt[0] = gid
	for ok && i < len(rlt) {
		if gid, ok = gmap.mm[gid]; ok {
			rlt[i] = gid
			i++
		}
	}

	unlock(&gmap.lock)

	return i
}


func calcgid(gg *g) int64 {
	return gg.goid
	// return gg.forTrace
}


func Getgid() int64 {
	_g_ := getg()
	return calcgid(_g_)
}
*/
//

// var _forTrace uint64 = uint64(0xFFFFFFFFFFFFFFFF)

// func genForTrace(gg *g) int64 {
// _forTrace = atomic.Xadd64(&_forTrace, -1)
// return int64(_forTrace)
// return gg.goid
// }

//
