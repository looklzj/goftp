package controllers

import (
	"anime/models"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/orm"
)

type BaseController struct {
	beego.Controller
}

func (c *BaseController)Prepare() {
	tags:=[]*models.Tag{}
	orm.NewOrm().QueryTable("tag").All(&tags)
	c.Data["tags"]=tags
}
