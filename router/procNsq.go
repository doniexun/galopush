package main

import (
	"encoding/json"
	"galopush/logs"
	"galopush/protocol"
)

//NsqHandler NSQ CHANNEL句柄
//根据不同的主题和数据进行分发
func (p *Router) NsqHandler(topic string, i interface{}) {
	defer func() {
		if r := recover(); r != nil {
			logs.Logger.Error("recover ", r)
		}
	}()
	b := i.([]byte)
	logs.Logger.Info("[nsq] topic=", topic, " msg=", string(b[:]))

	switch topic {
	case p.topics[4]: //sessionTimeout
		var bOnline bool
		var msg SessionTimeout
		if err := json.Unmarshal(b, &msg); err != nil {
			logs.Logger.Error(err, string(b[:]))
			return
		}
		logs.Logger.Debug("sessionTimeout ", msg)
		sess := p.pool.findSessions(msg.Uid)
		if sess != nil {
			for _, v := range sess.item {
				//找到对应终端类型
				if v.plat == msg.Termtype {
					if v.online == true {
						bOnline = true
					}
				}
			}
		}

		msg.Flag = bOnline
		buff, _ := json.Marshal(&msg)
		if err := p.producer.Publish(msg.Topic, buff); err != nil {
			logs.Logger.Error("sessionTimeout push to nsq Failed ", err, " msg=", string(buff[:]))
		}
	case p.topics[0]: //userOnlineState
		{
			var msg UserOnlineState
			if err := json.Unmarshal(b, &msg); err != nil {
				logs.Logger.Error(err, string(b[:]))
				return
			}

			//查找session
			sess := p.pool.findSessions(msg.Uid)
			if sess != nil {
				var find bool
				for _, v := range sess.item {
					//找到对应终端类型
					if v.plat == msg.Termtype {
						find = true
						v.authCode = msg.Code
						v.deviceToken = msg.DeviceToken
						v.login = msg.Login
						logs.Logger.Debug("UserState Find Item=", v, " uid=", msg.Uid)

						//socket在线 用户在线
						if v.online == true && msg.Login == true {
							//踢人
							c := p.pool.findComet(sess.cometId)
							if c != nil {
								logs.Logger.Debug("UserState Kick Because repeat login id=", sess.id, " palt=", v.plat)
								c.rpcClient.Kick(sess.id, v.plat, protocol.KICK_REASON_REPEAT)
							}
						}

						//socket在线 用户离线
						if v.online == true && msg.Login == false {
							//踢人
							c := p.pool.findComet(sess.cometId)
							if c != nil {
								logs.Logger.Debug("UserState Kick Because unlogin id=", sess.id, " palt=", v.plat)
								c.rpcClient.Kick(sess.id, v.plat, protocol.KICK_REASON_MUTEX)
							}
						}
					} else {
						//处理ANDROID IOS互斥
						if v.plat|msg.Termtype <= 0x03 {
							if v.login == true && msg.Login == true {
								//踢人
								c := p.pool.findComet(sess.cometId)
								if c != nil {
									logs.Logger.Debug("UserState Kick Because mutex id=", sess.id, " palt=", v.plat)
									c.rpcClient.Kick(sess.id, v.plat, protocol.KICK_REASON_MUTEX)
								}
							}
						}
					}
				}
				//有session但无对应终端类型
				if find == false {
					var it item
					it.plat = msg.Termtype
					it.online = false
					it.authCode = msg.Code
					it.deviceToken = msg.DeviceToken
					it.login = msg.Login
					sess.item = append(sess.item, &it)
					logs.Logger.Debug("UserState New Item=", it, " uid=", msg.Uid)
				}
			} else {
				//没有找到session
				sess = new(session)
				sess.id = msg.Uid
				var it item
				it.plat = msg.Termtype
				it.authCode = msg.Code
				it.deviceToken = msg.DeviceToken
				it.login = msg.Login
				sess.item = append(sess.item, &it)
				p.pool.insertSessions(msg.Uid, sess)
				logs.Logger.Debug("UserState New session uid=", msg.Uid, " item=", it)
			}
		}
	case p.topics[1], p.topics[2], p.topics[3]: //push callback message
		{
			var msg MsgDownward
			if err := json.Unmarshal(b, &msg); err != nil {
				logs.Logger.Error(err, string(b[:]))
				return
			}

			msgType := 0
			if topic == "push" {
				msgType = protocol.MSGTYPE_PUSH
			} else if topic == "callback" {
				msgType = protocol.MSGTYPE_CALLBACK
			} else if topic == "msgDownward" {
				msgType = protocol.MSGTYPE_MESSAGE
			}

			for _, receiver := range msg.Receivers {
				sess := p.pool.findSessions(receiver.Uid)
				if sess != nil {
					comet := p.pool.findComet(sess.cometId)
					if comet != nil {
						for _, it := range sess.item {
							if it.plat&receiver.Termtype > 0 {
								err := comet.rpcClient.Push(msgType, sess.id, it.plat, msg.Body)
								if err != nil {
									logs.Logger.Error(err)
									if it.plat != PLAT_WEB { //web不存离线
										p.SaveOfflineMsg(msgType, sess.id, it.plat, msg.Body)
									}
								}
							}
						}
					} else {
						logs.Logger.Debug("Push Failed comet offline comet=", sess.cometId)
						//comet offline need save msg
						for _, it := range sess.item {
							if it.plat&receiver.Termtype > 0 {
								if it.plat != PLAT_WEB { //web不存离线
									p.SaveOfflineMsg(msgType, sess.id, it.plat, msg.Body)
								}
							}
						}
					}
				} else {
					logs.Logger.Debug("Push Failed not find session id=", receiver.Uid)
				}
			}
		}
	}
}

func (p *Router) SaveOfflineMsg(msgType int, id string, termtype int, msg string) {
	switch msgType {
	case protocol.MSGTYPE_PUSH:
		p.store.SavePushMsg(id, []byte(msg))
	case protocol.MSGTYPE_CALLBACK:
		p.store.SaveCallbackMsg(id, termtype, []byte(msg))
	case protocol.MSGTYPE_MESSAGE:
		p.store.SaveImMsg(id, termtype, []byte(msg))

	}
}
