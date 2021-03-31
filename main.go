package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/rpc"
	"sync"
	"time"
)

// 1.实现3节点选举
// 2.改造代码成分布式选举代码,加入RPC调用
// 3.演示完整代码，自动选主 日志复制

// 定义3节点常量
const (
	raftCount = 3
)

// 声明Leader对象
type Leader struct {
	// 任期
	Term int
	// Leader编号
	LeaderId int
}

// 声明Raft
type Raft struct {
	// 锁
	mu sync.Mutex
	// 节点编号
	me int
	// 当前任期
	currentTerm int
	// 为哪个节点投票
	votedFor int
	// 3个状态
	// 0: Follower
	// 1: Candidate
	// 2: Leader
	state int
	// 发送最后一条数据的时间
	lastMessageTime int64
	// 当前Leader
	currentLeader int
	// 节点间发送信息的通道
	message chan bool
	// 选举的通道
	electCh chan bool
	// 心跳信号通道
	heartBeat chan bool
	// 返回心跳信号通道
	heartbeatRe chan bool
	// 超时时间（随机值）
	timeout int
}

var wg sync.WaitGroup

// 0：还没上任，-1：没有编号（说明Leader对象未产生）
var leader = Leader{0, -1}

// 创建节点
func Make(me int) *Raft {
	rf := &Raft{}
	rf.me = me
	// -1代表谁都不投，此时节点刚创建
	rf.votedFor = -1
	// 0 Follower
	rf.state = 0
	rf.timeout = 0
	rf.currentLeader = -1
	// 节点任期
	rf.setTerm(0)

	// 初始化通道
	rf.message = make(chan bool)
	rf.electCh = make(chan bool)
	rf.heartBeat = make(chan bool)
	rf.heartbeatRe = make(chan bool)

	// 设置随机种子
	rand.Seed(time.Now().UnixNano())

	// 选举的携程
	go rf.election()

	// 心跳检查的携程
	go rf.sendLeaderHeartBeat()

	return rf
}

func (rf *Raft) setTerm(term int) {
	rf.currentTerm = term
}

// 随机值
func randRange(min, max int64) int64 {
	return rand.Int63n(max-min) + min
}

// 获取当前时间，发送最后一条数据的时间
func millisecond() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// 实现选主的逻辑
func (rf *Raft) electionOneLeader(leader *Leader) bool {
	// 定义超时
	timeout := int64(100)
	// 投票数量
	var vote int
	// 定义是否开始心跳信号的产生
	var triggerHeartbeat bool

	// 时间
	last := millisecond()

	// 用于返回值
	success := false
	// 给当前节点变成candidate
	rf.mu.Lock()
	// 修改状态，让它称为候选人
	rf.becomeCandidate()
	rf.mu.Unlock()
	fmt.Println("start election leader")
	// 选举leader
	for {
		// 遍历所有节点拉选票
		for i := 0; i < raftCount; i++ {
			if i != rf.me {
				// 拉选票
				go func() {
					// LeaderId < 0说明没有Leader对象
					if leader.LeaderId < 0 {
						// 设置投票
						rf.electCh <- true
					}
				}()
			}
		}
		// 设置投票数量，设置1是因为自己给自己加了个1
		vote = 1
		// 遍历节点加选票
		for i := 0; i < raftCount; i++ {
			// 计算投票数量
			select {
			case ok := <-rf.electCh:
				// 如果是true，票数+1
				if ok {
					vote++
					// 判断是否能成为Leader, 票数大于一半的话success返回true
					success := vote > raftCount/2
					if success && !triggerHeartbeat {
						// 变化成主节点，选主成功了
						// 开始触发心跳信号检测
						triggerHeartbeat = true
						rf.mu.Lock()
						// 变主
						rf.becomeLeader()
						rf.mu.Unlock()
						// 由leader向其它节点发送心跳信号，确认Follower都有谁
						rf.heartBeat <- true
						fmt.Printf("%d号节点成为了leader \n", rf.me)
						fmt.Println("leader开始发送心跳信号了")
					}
				}
			}
		}
		// 做最后校验工作
		// 若不超时，且票数大于一半，则选举成功，break
		if timeout+last < millisecond() || (vote > raftCount/2 || rf.currentLeader > -1) {
			break
		} else {
			// 等待操作
			select {
			case <-time.After(time.Duration(10) * time.Millisecond):
			}
		}
	}
	return success
}

// Leader节点发送心跳信号
// 顺便完成数据同步（先不实现）
// 看小弟挂没挂
func (rf *Raft) sendLeaderHeartBeat() {
	// 死循环
	for {
		select {
		case <-rf.heartBeat:
			rf.sendAppendEntriesImp()
		}
	}
}

// 用于返回给leader的确认信号
func (rf *Raft) sendAppendEntriesImp() {
	// 是主就别跑了
	if rf.currentLeader == rf.me {
		// 此时是leader
		// 记录确认信号的节点个数
		var success_cout = 0
		// 设置确认信号
		for i := 0; i < raftCount; i++ {
			if i != rf.me {
				go func() {
					//rf.heartbeatRe <- true
					// 这里实际上相当于客户端
					rp, err := rpc.DialHTTP("tcp", "127.0.0.1:8080")
					if err != nil {
						log.Fatal(err)
					}
					// 接收服务器返回的信息
					// 接收服务端返回信息的变量
					ok := false
					err = rp.Call("Raft.Communication", Param{"hello"}, &ok)
					if err != nil {
						log.Fatal(err)
					}
					if ok {
						rf.heartbeatRe <- true
					}

				}()
			}
		}
		// 计算返回确认信号个数
		for i := 0; i < raftCount; i++ {
			select {
			case ok := <-rf.heartbeatRe:
				if ok {
					success_cout++
					if success_cout > raftCount/2 {
						fmt.Println("投票选举成功，心跳信号Ok")
						log.Fatal("程序结束")
					}
				}
			}
		}
	}
}

// 首字母大写，RPC规范
// 分布式通信
type Param struct {
	Msg string
}

// 通信方法
func (r *Raft)Communication(p Param, a *bool) error {
	fmt.Println(p.Msg)
	*a = true
	return nil
}

// 修改状态candidate
func (rf *Raft) becomeCandidate() {
	rf.state = 1
	// 每次选出来当前任期加1
	rf.setTerm(rf.currentTerm + 1)
	// 当前节点选主的时候给自己投票
	rf.votedFor = rf.me
	rf.currentLeader = -1
}

// 修改状态leader
func (rf *Raft) becomeLeader() {
	rf.state = 2
	rf.currentLeader = rf.me
}

// 选举
func (rf *Raft) election() {
	// 设置标记，判断是否选出了Leader
	var result bool

	for {
		// 设置超时, 150到300随机数
		timeout := randRange(150, 300)
		rf.lastMessageTime = millisecond()
		select {
		// 延迟等待1毫秒
		case <-time.After(time.Duration(timeout) * time.Millisecond):
			fmt.Println("当前节点状态为:", rf.state)
		}
		//result = false
		for !result {
			// 选主逻辑
			result = rf.electionOneLeader(&leader)
		}

	}
}

func main() {
	// 过程，有三个节点，最初都是Follower
	// 若有Candidate状态，进行投票拉票
	// 会产生Leader

	wg.Add(3)
	// 创建3个节点
	for i := 0; i < raftCount; i++ {
		// 创建3个raft节点
		go Make(i)
	}

	// 加入服务端监听
	rpc.Register(new(Raft))
	rpc.HandleHTTP()
	// 监听服务
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal(err)
	}

	wg.Wait()

}
